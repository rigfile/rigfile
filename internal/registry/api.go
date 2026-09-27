package registry

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rigfile/rigfile/internal/sigverify"
)

func (s *Server) apiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/search", s.apiSearch)
	mux.HandleFunc("GET /v1/rigs/{owner}/{name}", s.apiRig)
	mux.HandleFunc("GET /v1/rigs/{owner}/{name}/resolve", s.apiResolve)
	mux.HandleFunc("GET /v1/rigs/{owner}/{name}/diff", s.apiDiff)
	mux.HandleFunc("GET /v1/rigs/{owner}/{name}/derived", s.apiDerived)
	mux.HandleFunc("GET /v1/rigs/{owner}/{name}/versions/{version}", s.apiVersion)
	mux.HandleFunc("GET /v1/rigs/{owner}/{name}/versions/{version}/manifest", s.apiManifest)
	mux.HandleFunc("GET /v1/rigs/{owner}/{name}/versions/{version}/tarball", s.apiTarball)
	mux.HandleFunc("GET /v1/rigs/{owner}/{name}/versions/{version}/trust", s.apiTrust)
	mux.HandleFunc("GET /v1/rigs/{owner}/{name}/versions/{version}/bundle", s.apiBundle)
	mux.HandleFunc("POST /v1/rigs/{owner}/{name}/versions", s.apiUpload)
	mux.HandleFunc("POST /v1/rigs/{owner}/{name}/versions/{version}/yank", s.apiYank)
	mux.HandleFunc("POST /v1/rigs/{owner}/{name}/visibility", s.apiVisibility)
	mux.HandleFunc("PUT /v1/rigs/{owner}/{name}/star", s.apiStar(true))
	mux.HandleFunc("DELETE /v1/rigs/{owner}/{name}/star", s.apiStar(false))
}

// viewer authenticates optionally: anonymous when there is no token, 401 when a token is presented but invalid.
func (s *Server) viewer(w http.ResponseWriter, r *http.Request) (Viewer, *User, bool) {
	u, ok := s.tokenUser(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="rigfile"`)
		apiError(w, http.StatusUnauthorized, "the token is not valid; run `rigfile login`")
		return Viewer{}, nil, false
	}
	return ViewerOf(u), u, true
}

type versionJSON struct {
	Version    string            `json:"version"`
	Status     string            `json:"status"`
	SHA256     string            `json:"tarball_sha256"`
	Size       int64             `json:"size"`
	CreatedAt  time.Time         `json:"created_at"`
	YankReason string            `json:"yank_reason,omitempty"`
	Targets    []string          `json:"targets,omitempty"`
	Secrets    []string          `json:"needs_secrets,omitempty"`
	Logins     []string          `json:"needs_logins,omitempty"`
	Layers     []string          `json:"layers,omitempty"`
	Findings   []Finding         `json:"findings,omitempty"`
	Warnings   []Finding         `json:"warnings,omitempty"`
	HeldReason string            `json:"held_reason,omitempty"`
	Analysis   []AnalysisFinding `json:"analysis,omitempty"`
	SimilarTo  []SimilarRig      `json:"similar_to,omitempty"`
}

func versionToJSON(v Version, owner bool) versionJSON {
	j := versionJSON{Version: v.Version, Status: v.Status, SHA256: v.TarballSHA256, Size: v.Size, CreatedAt: v.CreatedAt, YankReason: v.YankReason,
		Targets: v.Targets, Secrets: v.NeedsSecrets, Logins: v.NeedsLogins, Layers: v.Layers}
	if owner { // findings are for the publisher (and admins); other people only learn that a version is published
		j.Findings, j.Warnings, j.HeldReason = v.Findings, v.Warnings, v.HeldReason
	}
	j.Analysis, j.SimilarTo = v.Analysis, v.SimilarTo
	return j
}

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "search", 120, 30) {
		return
	}
	v, _, ok := s.viewer(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rs, err := s.Store.Search(r.Context(), trunc(r.URL.Query().Get("q"), 100), limit, v)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "search failed")
		return
	}
	out := make([]map[string]any, 0, len(rs))
	for _, x := range rs {
		out = append(out, map[string]any{"owner": x.Owner, "name": x.Name, "description": x.Description, "latest": x.Latest, "stars": x.Stars})
	}
	writeJSON(w, http.StatusOK, map[string]any{"rigs": out})
}

func (s *Server) apiRig(w http.ResponseWriter, r *http.Request) {
	v, u, ok := s.viewer(w, r)
	if !ok {
		return
	}
	rig, err := s.Store.GetRig(r.Context(), r.PathValue("owner"), r.PathValue("name"), v)
	if err != nil {
		apiError(w, http.StatusNotFound, "no such rig")
		return
	}
	vs, err := s.Store.ListVersions(r.Context(), rig.ID, v)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "could not list versions")
		return
	}
	isOwner := s.Store.CanManage(r.Context(), u, rig)
	_, derivedCount, _ := s.Store.Derived(r.Context(), rig.Owner, rig.Name, v, 1)
	list := make([]versionJSON, 0, len(vs))
	latest := ""
	for _, x := range vs {
		list = append(list, versionToJSON(x, isOwner))
		if latest == "" && x.Status == "published" {
			latest = x.Version
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"owner": rig.Owner, "name": rig.Name, "description": rig.Description, "visibility": rig.Visibility,
		"stars": rig.Stars, "latest": latest, "versions": list, "derived": derivedCount})
}

// apiDerived lists the public rigs built on this one. The rig itself must be visible to the viewer.
func (s *Server) apiDerived(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.viewer(w, r)
	if !ok {
		return
	}
	rig, err := s.Store.GetRig(r.Context(), r.PathValue("owner"), r.PathValue("name"), v)
	if err != nil {
		apiError(w, http.StatusNotFound, "no such rig")
		return
	}
	list, total, err := s.Store.Derived(r.Context(), rig.Owner, rig.Name, v, 50)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "could not list derived rigs")
		return
	}
	type item struct {
		Owner       string `json:"owner"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Stars       int    `json:"stars"`
		Latest      string `json:"latest"`
	}
	out := make([]item, 0, len(list))
	for _, x := range list {
		out = append(out, item{x.Owner, x.Name, x.Description, x.Stars, x.Latest})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "rigs": out})
}

func (s *Server) apiResolve(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "resolve", 240, 60) {
		return
	}
	v, _, ok := s.viewer(w, r)
	if !ok {
		return
	}
	rng := r.URL.Query().Get("range")
	// an exact version is looked up directly, so a yanked version still resolves (lockfiles keep working)
	if versionRe.MatchString(rng) {
		if _, ver, err := s.Store.GetVersion(r.Context(), r.PathValue("owner"), r.PathValue("name"), rng, v); err == nil && (ver.Status == "published" || ver.Status == "yanked") {
			writeJSON(w, http.StatusOK, versionToJSON(*ver, false))
			return
		}
		apiError(w, http.StatusNotFound, "no such version")
		return
	}
	_, ver, err := s.Store.Resolve(r.Context(), r.PathValue("owner"), r.PathValue("name"), rng, v)
	if err != nil {
		apiError(w, http.StatusNotFound, "no published version satisfies that")
		return
	}
	writeJSON(w, http.StatusOK, versionToJSON(*ver, false))
}

func (s *Server) apiVersion(w http.ResponseWriter, r *http.Request) {
	v, u, ok := s.viewer(w, r)
	if !ok {
		return
	}
	rig, ver, err := s.Store.GetVersion(r.Context(), r.PathValue("owner"), r.PathValue("name"), r.PathValue("version"), v)
	if err != nil {
		apiError(w, http.StatusNotFound, "no such version")
		return
	}
	writeJSON(w, http.StatusOK, versionToJSON(*ver, s.Store.CanManage(r.Context(), u, rig)))
}

// pullable finds a version the viewer may download: published or yanked (or, for the owner, any they can see).
func (s *Server) pullable(w http.ResponseWriter, r *http.Request) (*Rig, *Version, bool) {
	v, u, ok := s.viewer(w, r)
	if !ok {
		return nil, nil, false
	}
	rig, ver, err := s.Store.GetVersion(r.Context(), r.PathValue("owner"), r.PathValue("name"), r.PathValue("version"), v)
	if err != nil {
		apiError(w, http.StatusNotFound, "no such version")
		return nil, nil, false
	}
	if ver.Status != "published" && ver.Status != "yanked" && !s.Store.CanManage(r.Context(), u, rig) {
		apiError(w, http.StatusNotFound, "no such version")
		return nil, nil, false
	}
	return rig, ver, true
}

func (s *Server) apiTrust(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "trust", 240, 60) {
		return
	}
	rig, ver, ok := s.pullable(w, r)
	if !ok {
		return
	}
	t, err := s.Store.Trust(r.Context(), rig, ver)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "could not gather the facts")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) apiManifest(w http.ResponseWriter, r *http.Request) {
	_, ver, ok := s.pullable(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, ver.ManifestYAML)
}

func (s *Server) apiTarball(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "download", 120, 30) {
		return
	}
	rig, ver, ok := s.pullable(w, r)
	if !ok {
		return
	}
	rc, size, err := s.Blobs.Get(r.Context(), ver.TarballSHA256)
	if err != nil {
		s.Log.Error("blob missing", "version", ver.ID, "err", err)
		apiError(w, http.StatusInternalServerError, "the file is unavailable")
		return
	}
	defer rc.Close()
	h := w.Header()
	h.Set("Content-Type", "application/gzip")
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	h.Set("X-Rigfile-SHA256", ver.TarballSHA256)
	h.Set("ETag", `"`+ver.TarballSHA256+`"`)
	h.Set("Content-Disposition", `attachment; filename="`+rig.Name+"-"+ver.Version+`.tar.gz"`)
	if ver.Status == "yanked" {
		h.Set("X-Rigfile-Yanked", "true")
	}
	if rig.Visibility == "public" {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, no-store")
	}
	_, _ = io.Copy(w, rc)
}

func (s *Server) apiUpload(w http.ResponseWriter, r *http.Request) {
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	if paused, why := s.Store.PublishingPaused(r.Context()); paused {
		w.Header().Set("Retry-After", "3600")
		apiError(w, http.StatusServiceUnavailable, "publishing is paused: "+why)
		return
	}
	if !s.Lim.Allow("upload|"+strconv.FormatInt(u.ID, 10), 6, 10) {
		w.Header().Set("Retry-After", "60")
		apiError(w, http.StatusTooManyRequests, "too many uploads; slow down")
		return
	}
	owner, name := r.PathValue("owner"), r.PathValue("name")
	if !ownerRe.MatchString(owner) || !rigNameRe.MatchString(name) {
		apiError(w, http.StatusBadRequest, "owner and name must be lowercase letters, digits and hyphens (names may also contain . and _)")
		return
	}
	if owner != u.Login && !u.IsAdmin {
		// an organisation's namespace is open to its members; anyone else is told the same thing as for another person's name
		if org, err := s.Store.OrgByLogin(r.Context(), owner); err != nil || s.Store.OrgRole(r.Context(), u, org.ID) == "" {
			apiError(w, http.StatusForbidden, "you can publish only under your own name ("+u.Login+"/...) or an organisation you belong to")
			return
		}
	}
	if IsReservedOwner(owner) && !u.IsAdmin {
		apiError(w, http.StatusForbidden, "the "+owner+"/ namespace is reserved")
		return
	}
	ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if ct != "application/gzip" && ct != "application/x-gzip" && ct != "multipart/form-data" {
		apiError(w, http.StatusUnsupportedMediaType, "send the rig as a gzip tarball (Content-Type: application/gzip), or as multipart/form-data with `tarball` and an optional Sigstore `bundle`")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.MaxUpload+512<<10)
	var data, bundleJSON []byte
	var err error
	if ct == "multipart/form-data" {
		data, bundleJSON, err = readMultipart(r, s.Cfg.MaxUpload)
	} else {
		data, err = io.ReadAll(io.LimitReader(r.Body, s.Cfg.MaxUpload+1))
		if err == nil && int64(len(data)) > s.Cfg.MaxUpload {
			err = &http.MaxBytesError{}
		}
	}
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			apiError(w, http.StatusRequestEntityTooLarge, "the upload is larger than "+strconv.FormatInt(s.Cfg.MaxUpload>>20, 10)+" MiB")
			return
		}
		apiError(w, http.StatusBadRequest, "could not read the upload: "+err.Error())
		return
	}
	info, err := validateUpload(data, owner, name)
	if err != nil {
		var ue *UploadError
		if errors.As(err, &ue) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ue.Msg, "problems": ue.Problems})
			return
		}
		s.Log.Error("upload validation", "err", err)
		apiError(w, http.StatusInternalServerError, "could not check the upload")
		return
	}
	if len(bundleJSON) > 0 {
		verifyFn := s.VerifySignature
		if verifyFn == nil {
			tm, err := s.trustedRoot()
			if err != nil {
				s.Log.Error("sigstore root", "err", err)
				apiError(w, http.StatusServiceUnavailable, "signatures cannot be verified right now; try again later or upload without one")
				return
			}
			verifyFn = func(b, a []byte) (*sigverify.Result, error) { return sigverify.Verify(tm, b, a) }
		}
		res, err := verifyFn(bundleJSON, data)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the signature does not verify", "problems": []string{err.Error()}})
			return
		}
		info.Bundle, info.SignerIssuer, info.SignerSubject = string(bundleJSON), res.Issuer, res.Subject
		info.SignerIsPublisher = sigverify.PublisherIdentity(res.Issuer, res.Subject, owner)
	}
	sha, _, err := s.Blobs.Put(r.Context(), bytes.NewReader(data), s.Cfg.MaxUpload)
	if err != nil || sha != info.TarballSHA {
		s.Log.Error("blob put", "err", err)
		apiError(w, http.StatusInternalServerError, "could not store the upload")
		return
	}
	info.UserID, info.Admin = u.ID, u.IsAdmin
	ver, err := s.Store.CreateVersion(r.Context(), info.NewVersion)
	switch {
	case errors.Is(err, ErrConflict):
		apiError(w, http.StatusConflict, owner+"/"+name+"@"+info.Version+" already exists; versions are immutable, publish a new version number")
		return
	case errors.Is(err, ErrForbidden):
		apiError(w, http.StatusForbidden, "you do not own "+owner+"/"+name)
		return
	case err != nil:
		s.Log.Error("create version", "err", err)
		apiError(w, http.StatusInternalServerError, "could not record the version")
		return
	}
	s.Store.Audit(r.Context(), u, "version.upload", owner+"/"+name+"@"+info.Version, map[string]any{"sha256": sha, "size": info.Size})
	writeJSON(w, http.StatusAccepted, map[string]any{"owner": owner, "name": name, "version": ver.Version, "status": "pending", "tarball_sha256": sha})
}

// readMultipart reads the `tarball` and optional `bundle` parts of a signed upload, each with its own size cap.
func readMultipart(r *http.Request, maxTarball int64) (tarball, bundle []byte, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, err
	}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		limit := int64(256 << 10)
		if p.FormName() == "tarball" {
			limit = maxTarball
		}
		b, err := io.ReadAll(io.LimitReader(p, limit+1))
		if err != nil {
			return nil, nil, err
		}
		if int64(len(b)) > limit {
			return nil, nil, &http.MaxBytesError{}
		}
		switch p.FormName() {
		case "tarball":
			tarball = b
		case "bundle":
			bundle = b
		default:
			return nil, nil, errors.New("unexpected part " + p.FormName())
		}
	}
	if len(tarball) == 0 {
		return nil, nil, errors.New("the upload has no tarball part")
	}
	return tarball, bundle, nil
}

func (s *Server) apiBundle(w http.ResponseWriter, r *http.Request) {
	_, ver, ok := s.pullable(w, r)
	if !ok {
		return
	}
	b := s.Store.Bundle(r.Context(), ver.ID)
	if b == "" {
		apiError(w, http.StatusNotFound, "this version is not signed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, b)
}

func (s *Server) apiYank(w http.ResponseWriter, r *http.Request) {
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	_ = r.ParseForm()
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	if reason == "" || len(reason) > 300 {
		apiError(w, http.StatusBadRequest, "give a reason (up to 300 characters): reason=...")
		return
	}
	switch err := s.Store.Yank(r.Context(), r.PathValue("owner"), r.PathValue("name"), r.PathValue("version"), reason, u); {
	case errors.Is(err, ErrNotFound):
		apiError(w, http.StatusNotFound, "no such published version")
	case errors.Is(err, ErrForbidden):
		apiError(w, http.StatusForbidden, "only the owner can yank")
	case err != nil:
		apiError(w, http.StatusInternalServerError, "could not yank")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) apiVisibility(w http.ResponseWriter, r *http.Request) {
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	_ = r.ParseForm()
	switch err := s.Store.SetVisibility(r.Context(), r.PathValue("owner"), r.PathValue("name"), r.PostFormValue("visibility"), u); {
	case errors.Is(err, ErrNotFound):
		apiError(w, http.StatusNotFound, "no such rig")
	case errors.Is(err, ErrForbidden):
		apiError(w, http.StatusForbidden, "only the owner can change visibility")
	case errors.Is(err, ErrConflict):
		apiError(w, http.StatusConflict, err.Error())
	case err != nil:
		apiError(w, http.StatusBadRequest, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) apiStar(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.requireToken(w, r)
		if u == nil {
			return
		}
		if err := s.Store.SetStar(r.Context(), r.PathValue("owner"), r.PathValue("name"), u, on); err != nil {
			apiError(w, http.StatusNotFound, "no such rig")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
