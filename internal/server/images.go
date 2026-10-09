package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// The images sandboxes start from, managed by the operator (capability
// images): listed with what uses them, installed ahead of the first sandbox
// that wants one, removed when nothing does. An image is otherwise pulled by
// the create that first names it, which is how it always was and still is.
//
// Whoever may call this server may call these — on a plain sandboxd, the
// holder of its token, who is its operator. A gateway does not route them
// (phase 2 gives it an admin catalog): an install fills a node's disk for
// every tenant on it.
//
// An install runs in the background and outlives the request that asked for
// it: a pull of a few gigabytes is minutes. Its progress and, if it fails,
// why are in the listing; a failed one stays listed until it is installed or
// removed, or the server restarts.

// installTimeout bounds one install: the largest image over a slow link.
const installTimeout = time.Hour

type imageJob struct {
	state    string // api.ImageInstalling or api.ImageFailed
	progress api.ImageProgress
	err      string
}

type imageJobs struct {
	mu   sync.Mutex
	jobs map[string]*imageJob
}

func (s *Server) imageStore() (backend.ImageStore, bool) {
	st, ok := s.Backend.(backend.ImageStore)
	return st, ok
}

func (s *Server) needImages(w http.ResponseWriter) (backend.ImageStore, bool) {
	st, ok := s.imageStore()
	if !ok {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint does not manage images")
	}
	return st, ok
}

// imageUse is how many live sandboxes start from each image: running,
// suspended or still starting.
func (s *Server) imageUse() map[string]int {
	s.mu.Lock()
	recs := make([]*record, 0, len(s.sandboxes))
	for _, rec := range s.sandboxes {
		recs = append(recs, rec)
	}
	s.mu.Unlock()
	use := map[string]int{}
	for _, rec := range recs {
		if sb := rec.snapshot(); sb.State != api.StateTerminated {
			use[sb.Image]++
		}
	}
	return use
}

// pinned says why an image may not be removed: the server's default, or one
// it keeps a pool of.
func (s *Server) pinned(ref string) (isDefault, pooled bool) {
	for _, p := range s.Policy.Pools {
		img := p.Image
		if img == "" {
			img = s.Policy.DefaultImage
		}
		if img == ref {
			pooled = true
		}
	}
	return ref == s.Policy.DefaultImage, pooled
}

// listImages serves GET /v1/images: installed images, and those installing or
// failed, by name.
func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	st, ok := s.needImages(w)
	if !ok {
		return
	}
	installed, err := st.Images(r.Context())
	if err != nil {
		s.logf("listing images: %v", err)
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the images could not be listed")
		return
	}
	use := s.imageUse()
	byRef := map[string]*api.Image{}
	// An image a live sandbox runs from is on this host, whether or not the
	// backend's own list says so: a root disk from before disks recorded
	// their images, or one the runtime pulled for a container it ran.
	for ref := range use {
		byRef[ref] = &api.Image{Image: ref, State: api.ImageInstalled}
	}
	for _, in := range installed {
		img := &api.Image{Image: in.Ref, State: api.ImageInstalled, Digest: in.Digest, Bytes: in.Bytes}
		if !in.InstalledAt.IsZero() {
			t := in.InstalledAt.UTC()
			img.InstalledAt = &t
		}
		byRef[in.Ref] = img
	}
	s.images.mu.Lock()
	for ref, j := range s.images.jobs {
		if j.state == api.ImageFailed && byRef[ref] != nil {
			continue // installed after all, by a create
		}
		img := byRef[ref]
		if img == nil {
			img = &api.Image{Image: ref}
			byRef[ref] = img
		}
		img.State, img.Error = j.state, j.err
		if j.state == api.ImageInstalling {
			p := j.progress
			img.Progress = &p
		}
	}
	s.images.mu.Unlock()
	out := api.ImageList{Images: []api.Image{}}
	for ref, img := range byRef {
		img.InUse = use[ref]
		img.Default, img.Pooled = s.pinned(ref)
		out.Images = append(out.Images, *img)
	}
	sort.Slice(out.Images, func(i, j int) bool { return out.Images[i].Image < out.Images[j].Image })
	writeJSON(w, http.StatusOK, out)
}

// installImage serves POST /v1/images: 202 with the install under way, or
// the one already under way for that image.
func (s *Server) installImage(w http.ResponseWriter, r *http.Request) {
	st, ok := s.needImages(w)
	if !ok {
		return
	}
	var req api.InstallImageRequest
	if !decode(w, r, &req) {
		return
	}
	if err := spec.CheckImage(req.Image, s.Policy); err != nil {
		writeSpecErr(w, err)
		return
	}
	ref := req.Image
	s.images.mu.Lock()
	if s.images.jobs == nil {
		s.images.jobs = map[string]*imageJob{}
	}
	j := s.images.jobs[ref]
	if j == nil || j.state != api.ImageInstalling {
		j = &imageJob{state: api.ImageInstalling, progress: api.ImageProgress{Phase: "pulling"}}
		s.images.jobs[ref] = j
		go s.runInstall(st, ref, j)
	}
	out := api.Image{Image: ref, State: j.state, Progress: &api.ImageProgress{Phase: j.progress.Phase, Done: j.progress.Done, Total: j.progress.Total}}
	s.images.mu.Unlock()
	out.Default, out.Pooled = s.pinned(ref)
	writeJSON(w, http.StatusAccepted, out)
}

func (s *Server) runInstall(st backend.ImageStore, ref string, j *imageJob) {
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	start := time.Now()
	s.logf("image %s: installing", ref)
	err := st.InstallImage(ctx, ref, func(p backend.ImageProgress) {
		s.images.mu.Lock()
		j.progress = api.ImageProgress{Phase: p.Phase, Done: p.Done, Total: p.Total}
		s.images.mu.Unlock()
	})
	s.images.mu.Lock()
	defer s.images.mu.Unlock()
	if err != nil {
		s.logf("image %s: install failed after %s: %v", ref, time.Since(start).Round(time.Second), err)
		j.state, j.err = api.ImageFailed, err.Error()
		return
	}
	s.logf("image %s: installed in %s", ref, time.Since(start).Round(time.Second))
	if s.images.jobs[ref] == j {
		delete(s.images.jobs, ref)
	}
}

// removeImage serves DELETE /v1/images?image=REF.
func (s *Server) removeImage(w http.ResponseWriter, r *http.Request) {
	st, ok := s.needImages(w)
	if !ok {
		return
	}
	ref := r.URL.Query().Get("image")
	if err := spec.CheckImage(ref, spec.Policy{}); err != nil {
		writeSpecErr(w, err)
		return
	}
	if isDefault, pooled := s.pinned(ref); isDefault || pooled {
		writeErr(w, http.StatusConflict, api.CodeConflict, ref+" is this server's default or pooled image; change the policy to remove it")
		return
	}
	if n := s.imageUse()[ref]; n > 0 {
		writeErr(w, http.StatusConflict, api.CodeConflict, ref+" is what a running or suspended sandbox here starts from")
		return
	}
	s.images.mu.Lock()
	j := s.images.jobs[ref]
	if j != nil && j.state == api.ImageInstalling {
		s.images.mu.Unlock()
		writeErr(w, http.StatusConflict, api.CodeConflict, ref+" is being installed")
		return
	}
	failed := j != nil
	delete(s.images.jobs, ref)
	s.images.mu.Unlock()

	freed, err := st.RemoveImage(r.Context(), ref)
	switch {
	case errors.Is(err, backend.ErrNotFound) && failed:
		// Only its failed install was listed; that is what goes.
	case errors.Is(err, backend.ErrNotFound):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, ref+" is not installed here")
		return
	case errors.Is(err, backend.ErrBusy):
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
		return
	case err != nil:
		s.logf("image %s: removing: %v", ref, err)
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the image could not be removed")
		return
	default:
		s.logf("image %s: removed, freeing %d MiB", ref, freed>>20)
	}
	writeJSON(w, http.StatusOK, api.RemovedImage{Image: ref, FreedBytes: freed})
}
