package server

import (
	"net/http"
	"sort"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Volumes are named filesystems that outlive the sandboxes they are mounted
// in: a package cache, a dataset, a model's weights. The backend's storage is
// the record of which exist — sandboxd keeps no records across a restart, and a
// volume is the one thing that must survive one. Which live sandbox has a
// volume is the server's to know, from its own records; after a restart no
// sandbox is live, so none is attached.
//
// A volume is a filesystem the guest writes, and the next sandbox to mount it
// reads what the last one left. That is the feature, not a leak — but it is why
// the host never mounts one: a filesystem image an agent wrote is parsed by a
// guest kernel only.

func (s *Server) volumeStore(w http.ResponseWriter) (backend.VolumeStore, bool) {
	vs, ok := s.Backend.(backend.VolumeStore)
	if !ok || !s.Backend.Capabilities()[api.CapVolumes] {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint has no volumes")
		return nil, false
	}
	return vs, true
}

// attachedTo is the live sandbox a volume is mounted in, or "". Caller holds mu.
func (s *Server) attachedTo(name string) string {
	for _, rec := range s.sandboxes {
		sb := rec.snapshot()
		if sb.State == api.StateTerminated {
			continue
		}
		for _, m := range sb.Volumes {
			if m.Name == name {
				return sb.ID
			}
		}
	}
	return ""
}

// volumesExist answers a create naming volumes: the endpoint has them, and each
// one named exists.
func (s *Server) volumesExist(w http.ResponseWriter, r *http.Request, mounts []api.VolumeMount) bool {
	vs, ok := s.volumeStore(w)
	if !ok {
		return false
	}
	have, err := vs.Volumes(r.Context())
	if err != nil {
		writeBackendErr(w, err)
		return false
	}
	known := map[string]bool{}
	for _, v := range have {
		known[v.Name] = true
	}
	for _, m := range mounts {
		if !known[m.Name] {
			writeErr(w, http.StatusNotFound, api.CodeNotFound, "no volume named "+m.Name)
			return false
		}
	}
	return true
}

func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) {
	var req api.CreateVolumeRequest
	if !decode(w, r, &req) {
		return
	}
	vs, ok := s.volumeStore(w)
	if !ok {
		return
	}
	if !spec.ValidName(req.Name) {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "name: lowercase letters, digits and dashes, at most 63")
		return
	}
	size := req.SizeMB
	if size == 0 {
		size = s.Policy.DefaultDiskMB
	}
	if size < 1 || size > s.Policy.Limits.MaxDiskMB {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "size_mb: between 1 and the server's max_disk_mb")
		return
	}
	have, err := vs.Volumes(r.Context())
	if err != nil {
		writeBackendErr(w, err)
		return
	}
	for _, v := range have {
		if v.Name == req.Name {
			writeErr(w, http.StatusConflict, api.CodeConflict, "a volume named "+req.Name+" already exists")
			return
		}
	}
	if err := vs.CreateVolume(r.Context(), req.Name, size); err != nil {
		writeBackendErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.Volume{Name: req.Name, SizeMB: size, CreatedAt: s.now().UTC()})
}

func (s *Server) listVolumes(w http.ResponseWriter, r *http.Request) {
	vs, ok := s.volumeStore(w)
	if !ok {
		return
	}
	have, err := vs.Volumes(r.Context())
	if err != nil {
		writeBackendErr(w, err)
		return
	}
	out := make([]api.Volume, 0, len(have))
	s.mu.Lock()
	for _, v := range have {
		out = append(out, api.Volume{Name: v.Name, SizeMB: v.SizeMB, CreatedAt: v.CreatedAt, AttachedTo: s.attachedTo(v.Name)})
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, api.VolumeList{Volumes: out})
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	vs, ok := s.volumeStore(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !spec.ValidName(name) {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no volume named "+name)
		return
	}
	s.mu.Lock()
	if holder := s.attachedTo(name); holder != "" {
		s.mu.Unlock()
		writeErr(w, http.StatusConflict, api.CodeConflict, "volume "+name+" is attached to "+holder)
		return
	}
	if s.deleting == nil {
		s.deleting = map[string]bool{}
	}
	s.deleting[name] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.deleting, name)
		s.mu.Unlock()
	}()
	if err := vs.DeleteVolume(r.Context(), name); err != nil {
		writeBackendErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
