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
	"sort"
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
	StateDir    string // per-sandbox directories, snapshots
	// ImageDir holds the root disks built from images; default StateDir. With the
	// jailer it must be on the same filesystem as StateDir (disks are hard-linked).
	ImageDir string
	Puller   *image.Puller
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
	// Keep leaves VMs running when sandboxd exits (Detach) and takes back,
	// at start, the ones an earlier sandboxd left (keep.go). Without it a
	// start removes every VM it finds, as it always has.
	Keep bool
}

// Backend runs sandboxes as Firecracker microVMs.
type Backend struct {
	cfg Config

	mu  sync.Mutex
	vms map[string]*vm

	// volBoot serialises boots that share a volume, under the jailer only.
	// Each jailed VM's link to a volume is chowned to that VM's uid, and the
	// link is one inode with the volume itself: two VMs booting on one
	// read-only volume at once could each chown it before the other's VMM had
	// opened it, and one would be refused its own drive. The owner matters
	// only when the VMM opens the drive, which is done by the time the guest
	// answers; an open descriptor survives the next VM's chown.
	volBootMu sync.Mutex
	volBoot   map[string]*sync.Mutex

	// built are the image references a root disk has been built or found for
	// since this process started, under mu. The disks themselves are cached by
	// a digest of image and guest agent, which names no reference, so the
	// record of which references they serve is kept here. A restart forgets
	// it, which costs a gateway one hint, not a sandbox.
	built map[string]bool
}

// CachedImages is backend.ImageLister.
func (b *Backend) CachedImages() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.built))
	for ref := range b.built {
		out = append(out, ref)
	}
	return out
}

// lockVolumes takes the boot lock of each volume s mounts, in name order so
// two boots cannot each hold one the other wants, and returns its release.
func (b *Backend) lockVolumes(s backend.Spec) func() {
	if b.cfg.Jailer == nil || len(s.Volumes) == 0 {
		return func() {}
	}
	names := make([]string, 0, len(s.Volumes))
	for _, m := range s.Volumes {
		names = append(names, m.Name)
	}
	sort.Strings(names)
	var held []*sync.Mutex
	for _, n := range names {
		b.volBootMu.Lock()
		if b.volBoot == nil {
			b.volBoot = map[string]*sync.Mutex{}
		}
		l := b.volBoot[n]
		if l == nil {
			l = &sync.Mutex{}
			b.volBoot[n] = l
		}
		b.volBootMu.Unlock()
		l.Lock()
		held = append(held, l)
	}
	return func() {
		for _, l := range held {
			l.Unlock()
		}
	}
}

type vm struct {
	id     string
	dir    string // host-side state: console log, pid, snapshot files
	root   string // the jail's root, when there is one; else dir
	spec   backend.Spec
	client *guestproto.Client
	net    *tap // nil without networking
	// pid and start name the VMM: a pid alone can be reused once it exits,
	// and a VMM taken back from an earlier sandboxd is watched by both.
	pid       int
	start     uint64
	exited    chan struct{}
	suspended bool
	adopted   bool // taken back from an earlier sandboxd (keep.go)
}

// New checks the configuration and returns a backend. What an earlier
// sandboxd left behind it takes back when VMs are kept (Config.Keep, keep.go),
// and otherwise removes: without kept records, nothing could say whose a VM
// is, so neither may the VM survive.
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
	if cfg.ImageDir == "" {
		cfg.ImageDir = cfg.StateDir
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
	var kept []candidate
	if cfg.Keep {
		kept = b.findKept()
	} else {
		b.reapLeftovers()
	}
	// Whatever is still running or left from this state directory and is not
	// a VM being taken back goes, kept or not: a restart leaves nothing behind
	// that nobody manages.
	keptPIDs, keptJails := map[int]bool{}, map[string]bool{}
	for _, c := range kept {
		if !c.v.suspended {
			keptPIDs[c.v.pid] = true
		}
		keptJails[jailID(c.v.id)] = true
	}
	strays := b.sweepVMMs(keptPIDs)
	b.sweepJails(keptJails)
	b.pruneRootDisks(kept)
	if cfg.Network != nil {
		var taps []keptTap
		for _, c := range kept {
			if c.ks.Tap >= 0 {
				taps = append(taps, keptTap{id: c.v.id, idx: c.ks.Tap, policy: c.ks.Spec.Network})
			}
		}
		byID, err := cfg.Network.start(taps)
		if err != nil {
			return nil, err
		}
		for _, c := range kept {
			c.v.net = byID[c.v.id]
		}
	}
	if cfg.Keep || strays > 0 {
		cfg.Logf("after the restart: %d sandbox(es) taken back, %d stray VMM(s) ended", len(kept), strays)
	}
	for _, c := range kept {
		if cfg.Jailer != nil {
			cfg.Jailer.adopt(c.v.id, c.ks.UID)
		}
		b.vms[c.v.id] = c.v
		if b.built == nil {
			b.built = map[string]bool{}
		}
		b.built[c.v.spec.Image] = true
		cfg.Logf("sandbox %s: found running from an earlier run", c.v.id)
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
		api.CapSuspend:             true,
		api.CapMemorySnapshot:      true,
		api.CapTunnel:              true,
		api.CapVolumes:             true,
	}
}

func (b *Backend) get(id string) (*vm, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.vms[id]
	if !ok {
		return nil, backend.ErrNotFound
	}
	if v.suspended {
		return nil, backend.ErrBusy
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
	if s.FromSnapshot != "" {
		return b.createFromSnapshot(ctx, s)
	}
	rootfs, err := image.BuildRootFS(ctx, b.cfg.Puller, s.Image, b.cfg.Agent, b.cfg.ImageDir)
	if err != nil {
		b.cfg.Logf("sandbox %s: image %s: %v", s.ID, s.Image, err)
		return fmt.Errorf("image %s: %w", s.Image, err)
	}
	b.mu.Lock()
	if b.built == nil {
		b.built = map[string]bool{}
	}
	b.built[s.Image] = true
	b.mu.Unlock()
	if rootfs.OwnedByHost {
		b.cfg.Logf("sandbox %s: image %s was built without root, so its files are owned by the building user", s.ID, s.Image)
	}

	v, err := b.newVM(s)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			b.destroy(v)
		}
	}()

	scratch := filepath.Join(v.dir, "scratch.ext4")
	if err := image.MakeScratch(ctx, scratch, s.DiskMB); err != nil {
		return err
	}
	bootArgs := []string{
		"console=ttyS0", "reboot=k", "panic=1", "pci=off", "ro", FastBootArgs,
		"init=" + image.GuestAgentPath, "sbx.overlay=/dev/vdb",
		fmt.Sprintf("sbx.vsock=%d", guestPort), "sbx.hostname=" + hostname(s.ID),
	}
	if arg := volumeBootArg(s); arg != "" {
		bootArgs = append(bootArgs, arg)
	}
	unlockVolumes := b.lockVolumes(s)
	defer unlockVolumes()
	var volumes []string
	for _, m := range s.Volumes {
		p := b.volumePath(m.Name)
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("volume %s: %w", m.Name, backend.ErrNotFound)
		}
		volumes = append(volumes, p)
	}
	if b.cfg.Network != nil {
		t, err := b.cfg.Network.attach(s.ID, s.Network, b.ownerFor(s.ID))
		if err != nil {
			return fmt.Errorf("network: %w", err)
		}
		v.net = t
		bootArgs = append(bootArgs, t.kernelArgs()...)
	}

	// The sandbox's own files are named relative to the VMM's working directory
	// (its sandbox directory, or the jail's root), and the shared ones by
	// absolute path. A snapshot records these paths, so relative ones are what
	// let it be restored into another sandbox's directory.
	paths := vmPaths{Kernel: b.cfg.Kernel, RootFS: rootfs.Path, Scratch: "scratch.ext4", VsockUDS: "v.sock", Volumes: volumes}
	if b.cfg.Jailer != nil {
		if paths, err = b.cfg.Jailer.prepare(s.ID, b.cfg.Kernel, rootfs.Path, scratch, volumes); err != nil {
			return err
		}
	}
	cfgJSON, _ := json.MarshalIndent(BuildConfig(s, paths, strings.Join(bootArgs, " "), v.net), "", "  ")
	if err := os.WriteFile(v.hostPath("vm.json"), cfgJSON, 0o600); err != nil {
		return err
	}
	b.own(s.ID, v.hostPath("vm.json"))

	t0 := time.Now()
	if err := b.vmm(v, "--config-file", "vm.json"); err != nil {
		return err
	}
	if err := b.waitReady(ctx, v); err != nil {
		return err
	}
	b.cfg.Logf("sandbox %s: ready in %v", s.ID, time.Since(t0).Round(time.Millisecond))
	b.mu.Lock()
	b.vms[s.ID] = v
	b.mu.Unlock()
	b.saveKeep(v)
	return nil
}

// newVM makes a sandbox's directory and record.
func (b *Backend) newVM(s backend.Spec) (*vm, error) {
	dir := filepath.Join(b.sandboxesDir(), s.ID)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	v := &vm{id: s.ID, dir: dir, spec: s}
	if b.cfg.Jailer != nil {
		v.root = b.cfg.Jailer.root(s.ID)
	}
	return v, nil
}

// hostPath is where a file the VMM names relatively lives, as the host sees it.
func (v *vm) hostPath(name string) string {
	if v.root != "" {
		return filepath.Join(v.root, name)
	}
	return filepath.Join(v.dir, name)
}

func (b *Backend) own(id, path string) {
	if b.cfg.Jailer != nil {
		b.cfg.Jailer.own(id, path)
	}
}

// vmm starts the VMM — under the jailer when there is one — with its API
// socket and the given arguments, from the directory its relative paths are
// relative to.
func (b *Backend) vmm(v *vm, args ...string) error {
	_ = os.Remove(v.hostPath("v.sock"))
	_ = os.Remove(v.hostPath("api.sock"))
	console, err := os.OpenFile(filepath.Join(v.dir, "console.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer console.Close()
	var cmd *exec.Cmd
	if b.cfg.Jailer != nil {
		cmd = b.cfg.Jailer.command(v.id, b.cfg.Firecracker, args...)
	} else {
		cmd = exec.Command(b.cfg.Firecracker, append([]string{"--api-sock", "api.sock"}, args...)...)
		cmd.Dir = v.dir
	}
	cmd.Stdout, cmd.Stderr = console, console
	// A session of its own, not only a process group: a VMM must outlive
	// sandboxd when VMs are kept (keep.go), and a signal to sandboxd's
	// process group or session — Ctrl-C, a closed terminal — is not one
	// to its VMs. The VMM still leads its own group, which destroy kills.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	exited := make(chan struct{})
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the VMM: %w", err)
	}
	go func() { _ = cmd.Wait(); close(exited) }()
	start, _ := procStart(cmd.Process.Pid)
	v.pid, v.start, v.exited = cmd.Process.Pid, start, exited
	_ = os.WriteFile(filepath.Join(v.dir, "vmm.pid"), []byte(fmt.Sprint(v.pid)), 0o600)
	v.client = guestClient(v.hostPath("v.sock"))
	return nil
}

// guestClient talks to a VM's guest agent over its vsock socket.
func guestClient(uds string) *guestproto.Client {
	return &guestproto.Client{Dial: func(ctx context.Context) (io.ReadWriteCloser, error) {
		return vsock.DialFirecracker(ctx, uds, guestPort)
	}}
}

// waitReady waits for the guest agent, or for the VMM to die trying.
func (b *Backend) waitReady(ctx context.Context, v *vm) error {
	rctx, cancel := context.WithTimeout(ctx, b.cfg.BootTimeout)
	defer cancel()
	ready := make(chan error, 1)
	go func() { ready <- v.client.WaitReady(rctx) }()
	select {
	case err := <-ready:
		if err != nil {
			b.cfg.Logf("sandbox %s: guest agent did not answer; console:\n%s", v.id, tail(filepath.Join(v.dir, "console.log"), 4096))
			return fmt.Errorf("the guest did not come up: %w", err)
		}
		return nil
	case <-v.exited:
		b.cfg.Logf("sandbox %s: the VMM exited; console:\n%s", v.id, tail(filepath.Join(v.dir, "console.log"), 4096))
		return errors.New("the VMM exited before the guest came up")
	}
}

func (b *Backend) UpdateNetwork(_ context.Context, id string, p api.NetworkPolicy) error {
	v, err := b.get(id)
	if err != nil {
		return err
	}
	if v.net == nil {
		return errors.New("this sandbox has no network")
	}
	if err := b.cfg.Network.update(v.net, p); err != nil {
		return err
	}
	b.saveKeep(v)
	return nil
}

func (b *Backend) Terminate(_ context.Context, id string) error {
	b.mu.Lock()
	v, ok := b.vms[id]
	delete(b.vms, id)
	b.mu.Unlock()
	if ok {
		b.flushVolumes(v, true)
		b.destroy(v)
	}
	return nil
}

// destroy kills the VMM's process group and removes everything the sandbox had.
func (b *Backend) destroy(v *vm) {
	if v.exited != nil {
		killVMM(v)
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
		// The jail held a hard link to each volume, chowned to its uid; the
		// volume itself goes back to sandboxd once no VM has it.
		for _, m := range v.spec.Volumes {
			_ = os.Chown(b.volumePath(m.Name), os.Geteuid(), os.Getegid())
		}
	}
	_ = os.RemoveAll(v.dir)
}

func (b *Backend) Start(ctx context.Context, id string, p backend.ProcSpec, stdout, stderr io.Writer) (backend.Proc, error) {
	v, err := b.get(id)
	if err != nil {
		return nil, err
	}
	proc, err := v.client.ExecRequest(ctx, guestproto.Request{Argv: p.Argv, Env: p.Env, Cwd: p.Cwd, Tty: p.Tty, Rows: p.Rows, Cols: p.Cols,
		Keep: p.Keep, Session: p.Session}, stdout, stderr)
	if err != nil {
		return nil, mapErr(err)
	}
	return proc, nil
}

// Reattach is backend.Reattacher: it rejoins a kept process in the guest. A
// guest agent from before kept processes answers that it does not know the
// operation, which is the same as not holding the session.
func (b *Backend) Reattach(ctx context.Context, id, session string, stdout, stderr io.Writer, dropped func(int64)) (backend.Proc, error) {
	v, err := b.get(id)
	if err != nil {
		return nil, err
	}
	proc, err := v.client.Attach(ctx, session, 0, stdout, stderr, dropped)
	if guestproto.IsCode(err, guestproto.CodeNotFound) || guestproto.IsCode(err, guestproto.CodeBadRequest) {
		return nil, backend.ErrNotFound
	}
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
	case guestproto.CodeReadOnly:
		return backend.ErrReadOnly
	case guestproto.CodeNoSuchCommand:
		return backend.ErrNoSuchCmd
	case guestproto.CodeNoSuchCwd:
		return backend.ErrNoSuchCwd
	}
	return err
}

// --- the VM configuration ---------------------------------------------------

type vmPaths struct {
	Kernel, RootFS, Scratch, VsockUDS, APISock string
	// Volumes are the volume images, in the spec's order.
	Volumes []string
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
// always read-only; the scratch disk and the volumes not mounted read-only are
// the only writable ones, and a volume is never the root device.
func BuildConfig(s backend.Spec, p vmPaths, bootArgs string, t *tap) VMConfig {
	var c VMConfig
	c.BootSource.KernelImagePath = p.Kernel
	c.BootSource.BootArgs = bootArgs
	c.Drives = []drive{
		{DriveID: "rootfs", PathOnHost: p.RootFS, IsRootDevice: true, IsReadOnly: true},
		{DriveID: "scratch", PathOnHost: p.Scratch, IsReadOnly: false},
	}
	for i, path := range p.Volumes {
		ro := i < len(s.Volumes) && s.Volumes[i].ReadOnly
		c.Drives = append(c.Drives, drive{DriveID: fmt.Sprintf("vol%d", i), PathOnHost: path, IsReadOnly: ro})
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

// DialGuest opens a tunnel to a port on the guest's loopback.
func (b *Backend) DialGuest(ctx context.Context, id string, port int) (io.ReadWriteCloser, error) {
	v, err := b.get(id)
	if err != nil {
		return nil, err
	}
	conn, err := v.client.DialPort(ctx, port)
	return conn, mapErr(err)
}
