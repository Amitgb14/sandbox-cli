//go:build linux

package firecracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/vsock"
)

// FastBootArgs are the kernel arguments M3 measured: no boot log over the
// emulated serial port, no keyboard-controller probe. reboot=k still ends the VM.
const FastBootArgs = "quiet i8042.noaux i8042.nomux i8042.nopnp i8042.dumbkbd"

// guestPort is the vsock port the guest agent listens on.
const guestPort = 5000

// Config is how a host runs Firecracker.
type Config struct {
	Firecracker string // the VMM binary
	Kernel      string // guest kernel (vmlinux)
	Agent       string // sandbox-guestd built for the guest's architecture
	StateDir    string // image cache and per-sandbox directories
	Puller      *image.Puller
	// Network, when set, gives each sandbox a tap device with egress enforced on
	// the host. Nil means sandboxes have no network interface.
	Network *Network
	// Jailer, when set, runs each VMM under the jailer as an unprivileged uid in
	// a chroot. Needs root.
	Jailer *Jailer
	// BootTimeout bounds how long a create waits for the guest agent.
	BootTimeout time.Duration
	// Logf receives operator-facing detail (paths, console output) that API
	// errors deliberately leave out.
	Logf func(format string, a ...any)
}

// Backend runs sandboxes as Firecracker microVMs.
type Backend struct {
	cfg Config

	mu  sync.Mutex
	vms map[string]*vm
}

type vm struct {
	id     string
	dir    string
	cmd    *exec.Cmd
	client *guestproto.Client
	net    *tap // nil without networking
	exited chan struct{}
}

// New checks the configuration and returns a backend. It removes what an
// earlier sandboxd left behind: records do not survive a restart, so neither
// may the VMs they described.
func New(cfg Config) (*Backend, error) {
	for name, p := range map[string]string{"firecracker": cfg.Firecracker, "kernel": cfg.Kernel, "guest agent": cfg.Agent} {
		if p == "" {
			return nil, fmt.Errorf("firecracker backend: %s path is required", name)
		}
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("firecracker backend: %s: %w", name, err)
		}
	}
	if cfg.StateDir == "" {
		return nil, errors.New("firecracker backend: state directory is required")
	}
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		return nil, errors.New("firecracker backend: mkfs.ext4 is required (e2fsprogs)")
	}
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("firecracker backend: /dev/kvm: %w", err)
	}
	f.Close()
	if cfg.Puller == nil {
		cfg.Puller = &image.Puller{}
	}
	if cfg.Puller.Cache == "" {
		cfg.Puller.Cache = filepath.Join(cfg.StateDir, "images")
	}
	if cfg.BootTimeout == 0 {
		cfg.BootTimeout = 30 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	b := &Backend{cfg: cfg, vms: map[string]*vm{}}
	if err := os.MkdirAll(b.sandboxesDir(), 0o700); err != nil {
		return nil, err
	}
	b.reapLeftovers()
	if cfg.Network != nil {
		if err := cfg.Network.start(); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (b *Backend) sandboxesDir() string { return filepath.Join(b.cfg.StateDir, "sandboxes") }

// reapLeftovers kills VMs a previous sandboxd started and removes their files.
func (b *Backend) reapLeftovers() {
	des, _ := os.ReadDir(b.sandboxesDir())
	for _, de := range des {
		dir := filepath.Join(b.sandboxesDir(), de.Name())
		if pgid, err := readInt(filepath.Join(dir, "vmm.pid")); err == nil && pgid > 1 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		if b.cfg.Jailer != nil {
			b.cfg.Jailer.cleanup(de.Name())
		}
		_ = os.RemoveAll(dir)
		b.cfg.Logf("removed a sandbox left by an earlier run: %s", de.Name())
	}
}

func (b *Backend) Name() string { return "firecracker" }

func (b *Backend) Capabilities() map[string]bool {
	net := b.cfg.Network != nil
	return map[string]bool{
		api.CapNetworkPolicyUpdate: net,
		api.CapEgressAllowlist:     net,
		api.CapWorkspaceBundle:     true,
	}
}

func (b *Backend) get(id string) (*vm, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.vms[id]
	if !ok {
		return nil, backend.ErrNotFound
	}
	select {
	case <-v.exited:
		return nil, backend.ErrUnavailable
	default:
	}
	return v, nil
}

// Create builds (or reuses) the image's root disk, makes the scratch disk,
// starts the VMM and waits for the guest agent.
func (b *Backend) Create(ctx context.Context, s backend.Spec) (err error) {
	rootfs, err := image.BuildRootFS(ctx, b.cfg.Puller, s.Image, b.cfg.Agent, b.cfg.StateDir)
	if err != nil {
		b.cfg.Logf("sandbox %s: image %s: %v", s.ID, s.Image, err)
		return fmt.Errorf("image %s: %w", s.Image, err)
	}
	if rootfs.OwnedByHost {
		b.cfg.Logf("sandbox %s: image %s was built without root, so its files are owned by the building user", s.ID, s.Image)
	}

	dir := filepath.Join(b.sandboxesDir(), s.ID)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	v := &vm{id: s.ID, dir: dir, exited: make(chan struct{})}
	defer func() {
		if err != nil {
			b.destroy(v)
		}
	}()

	scratch := filepath.Join(dir, "scratch.ext4")
	if err := image.MakeScratch(ctx, scratch, s.DiskMB); err != nil {
		return err
	}
	bootArgs := []string{
		"console=ttyS0", "reboot=k", "panic=1", "pci=off", "ro", FastBootArgs,
		"init=" + image.GuestAgentPath, "sbx.overlay=/dev/vdb",
		fmt.Sprintf("sbx.vsock=%d", guestPort), "sbx.hostname=" + hostname(s.ID),
	}
	if b.cfg.Network != nil {
		t, err := b.cfg.Network.attach(s.ID, s.Network, b.ownerFor(s.ID))
		if err != nil {
			return fmt.Errorf("network: %w", err)
		}
		v.net = t
		bootArgs = append(bootArgs, t.kernelArgs()...)
	}

	paths := vmPaths{
		Kernel: b.cfg.Kernel, RootFS: rootfs.Path, Scratch: scratch,
		VsockUDS: filepath.Join(dir, "v.sock"), APISock: filepath.Join(dir, "api.sock"),
	}
	if b.cfg.Jailer != nil {
		if paths, err = b.cfg.Jailer.prepare(s.ID, paths); err != nil {
			return err
		}
	}
	cfg := BuildConfig(s, paths, strings.Join(bootArgs, " "), v.net)
	cfgJSON, _ := json.MarshalIndent(cfg, "", "  ")
	cfgPath := filepath.Join(dir, "vm.json")
	if b.cfg.Jailer != nil {
		cfgPath = b.cfg.Jailer.chrootPath(s.ID, "vm.json")
	}
	if err := os.WriteFile(cfgPath, cfgJSON, 0o600); err != nil {
		return err
	}
	if b.cfg.Jailer != nil {
		b.cfg.Jailer.own(s.ID, cfgPath)
	}

	console, err := os.OpenFile(filepath.Join(dir, "console.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer console.Close()
	if b.cfg.Jailer != nil {
		v.cmd = b.cfg.Jailer.command(s.ID, b.cfg.Firecracker)
	} else {
		v.cmd = exec.Command(b.cfg.Firecracker, "--api-sock", paths.APISock, "--config-file", cfgPath)
	}
	v.cmd.Stdout, v.cmd.Stderr = console, console
	v.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	t0 := time.Now()
	if err := v.cmd.Start(); err != nil {
		return fmt.Errorf("starting the VMM: %w", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "vmm.pid"), []byte(fmt.Sprint(v.cmd.Process.Pid)), 0o600)
	go func() { _ = v.cmd.Wait(); close(v.exited) }()

	udsHost := filepath.Join(dir, "v.sock")
	if b.cfg.Jailer != nil {
		udsHost = b.cfg.Jailer.chrootPath(s.ID, "v.sock")
	}
	v.client = &guestproto.Client{Dial: func(ctx context.Context) (io.ReadWriteCloser, error) {
		return vsock.DialFirecracker(ctx, udsHost, guestPort)
	}}
	rctx, cancel := context.WithTimeout(ctx, b.cfg.BootTimeout)
	defer cancel()
	ready := make(chan error, 1)
	go func() { ready <- v.client.WaitReady(rctx) }()
	select {
	case err := <-ready:
		if err != nil {
			b.cfg.Logf("sandbox %s: guest agent did not answer; console:\n%s", s.ID, tail(filepath.Join(dir, "console.log"), 4096))
			return fmt.Errorf("the guest did not come up: %w", err)
		}
	case <-v.exited:
		b.cfg.Logf("sandbox %s: the VMM exited during boot; console:\n%s", s.ID, tail(filepath.Join(dir, "console.log"), 4096))
		return errors.New("the VMM exited during boot")
	}
	b.cfg.Logf("sandbox %s: ready in %v", s.ID, time.Since(t0).Round(time.Millisecond))

	b.mu.Lock()
	b.vms[s.ID] = v
	b.mu.Unlock()
	return nil
}

func (b *Backend) UpdateNetwork(_ context.Context, id string, p api.NetworkPolicy) error {
	v, err := b.get(id)
	if err != nil {
		return err
	}
	if v.net == nil {
		return errors.New("this sandbox has no network")
	}
	return b.cfg.Network.update(v.net, p)
}

func (b *Backend) Terminate(_ context.Context, id string) error {
	b.mu.Lock()
	v, ok := b.vms[id]
	delete(b.vms, id)
	b.mu.Unlock()
	if ok {
		b.destroy(v)
	}
	return nil
}

// destroy kills the VMM's process group and removes everything the sandbox had.
func (b *Backend) destroy(v *vm) {
	if v.cmd != nil && v.cmd.Process != nil {
		_ = syscall.Kill(-v.cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-v.exited:
		case <-time.After(5 * time.Second):
		}
	}
	if v.net != nil {
		b.cfg.Network.detach(v.net)
	}
	if b.cfg.Jailer != nil {
		b.cfg.Jailer.cleanup(v.id)
	}
	_ = os.RemoveAll(v.dir)
}

func (b *Backend) Start(ctx context.Context, id string, p backend.ProcSpec, stdout, stderr io.Writer) (backend.Proc, error) {
	v, err := b.get(id)
	if err != nil {
		return nil, err
	}
	proc, err := v.client.Exec(ctx, p.Argv, p.Env, p.Cwd, stdout, stderr)
	if err != nil {
		return nil, mapErr(err)
	}
	return proc, nil
}

func (b *Backend) ReadFile(ctx context.Context, id, path string) ([]byte, error) {
	v, err := b.get(id)
	if err != nil {
		return nil, err
	}
	data, err := v.client.ReadFile(ctx, path)
	return data, mapErr(err)
}

func (b *Backend) WriteFile(ctx context.Context, id, path string, data []byte) error {
	v, err := b.get(id)
	if err != nil {
		return err
	}
	return mapErr(v.client.WriteFile(ctx, path, data))
}

func (b *Backend) Remove(ctx context.Context, id, path string) error {
	v, err := b.get(id)
	if err != nil {
		return err
	}
	return mapErr(v.client.Remove(ctx, path))
}

func (b *Backend) ListDir(ctx context.Context, id, path string) ([]api.DirEntry, error) {
	v, err := b.get(id)
	if err != nil {
		return nil, err
	}
	entries, err := v.client.List(ctx, path)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]api.DirEntry, 0, len(entries))
	for _, e := range entries {
		t := e.Type
		if t != "dir" {
			t = "file"
		}
		out = append(out, api.DirEntry{Name: e.Name, Type: t, Size: e.Size})
	}
	return out, nil
}

// mapErr turns guest errors into the backend's.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var ge *guestproto.Error
	if !errors.As(err, &ge) {
		return err
	}
	switch ge.Code {
	case guestproto.CodeNotFound:
		return backend.ErrNotFound
	case guestproto.CodeIsDir:
		return backend.ErrIsDir
	case guestproto.CodeNotDir:
		return backend.ErrNotDir
	case guestproto.CodeNotEmpty:
		return backend.ErrNotEmpty
	case guestproto.CodeNoSuchCommand:
		return backend.ErrNoSuchCmd
	}
	return err
}

// --- the VM configuration ---------------------------------------------------

type vmPaths struct {
	Kernel, RootFS, Scratch, VsockUDS, APISock string
}

// VMConfig is Firecracker's --config-file document.
type VMConfig struct {
	BootSource struct {
		KernelImagePath string `json:"kernel_image_path"`
		BootArgs        string `json:"boot_args"`
	} `json:"boot-source"`
	Drives        []drive       `json:"drives"`
	MachineConfig machineConfig `json:"machine-config"`
	NetIfaces     []netIface    `json:"network-interfaces,omitempty"`
	Vsock         vsockConfig   `json:"vsock"`
}

type drive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type machineConfig struct {
	VCPUCount  int `json:"vcpu_count"`
	MemSizeMiB int `json:"mem_size_mib"`
}

type netIface struct {
	IfaceID     string `json:"iface_id"`
	GuestMAC    string `json:"guest_mac"`
	HostDevName string `json:"host_dev_name"`
}

type vsockConfig struct {
	GuestCID int    `json:"guest_cid"`
	UDSPath  string `json:"uds_path"`
}

// BuildConfig renders a VM's configuration from a resolved spec. It is a pure
// function of its inputs — the property the golden test pins — and the only
// place a sandbox's resources, disks and devices are decided. The root disk is
// always read-only; the scratch disk is the only writable one.
func BuildConfig(s backend.Spec, p vmPaths, bootArgs string, t *tap) VMConfig {
	var c VMConfig
	c.BootSource.KernelImagePath = p.Kernel
	c.BootSource.BootArgs = bootArgs
	c.Drives = []drive{
		{DriveID: "rootfs", PathOnHost: p.RootFS, IsRootDevice: true, IsReadOnly: true},
		{DriveID: "scratch", PathOnHost: p.Scratch, IsReadOnly: false},
	}
	vcpus := int(math.Ceil(s.CPUs))
	if vcpus < 1 {
		vcpus = 1
	}
	c.MachineConfig = machineConfig{VCPUCount: vcpus, MemSizeMiB: s.MemoryMB}
	if t != nil {
		c.NetIfaces = []netIface{{IfaceID: "eth0", GuestMAC: t.mac, HostDevName: t.name}}
	}
	// Firecracker's vsock is backed by a unix socket on the host, not by
	// vhost-vsock, so the guest CID is local to the VM and every VM can use 3.
	c.Vsock = vsockConfig{GuestCID: 3, UDSPath: p.VsockUDS}
	return c
}

// hostname is the guest's hostname: the sandbox id, which is already a valid
// label (letters, digits, one underscore — replaced).
func hostname(id string) string { return strings.ReplaceAll(id, "_", "-") }

func readInt(p string) (int, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, err
	}
	var n int
	_, err = fmt.Sscan(strings.TrimSpace(string(b)), &n)
	return n, err
}

func tail(p string, n int64) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > n {
		_, _ = f.Seek(fi.Size()-n, io.SeekStart)
	}
	b, _ := io.ReadAll(io.LimitReader(f, n))
	return string(b)
}

// Close terminates every sandbox and removes the host network rules. sandboxd
// calls it on shutdown: a VM nobody can reach any more is one nobody can stop.
func (b *Backend) Close() {
	b.mu.Lock()
	vms := make([]*vm, 0, len(b.vms))
	for id, v := range b.vms {
		vms = append(vms, v)
		delete(b.vms, id)
	}
	b.mu.Unlock()
	for _, v := range vms {
		b.destroy(v)
	}
	if b.cfg.Network != nil {
		b.cfg.Network.Close()
	}
}
