package source

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Limits bound what an archive may unpack to (docs/sharing.md §4).
type Limits struct {
	MaxEntries  int
	MaxFileSize int64
	MaxTotal    int64
}

// DefaultLimits: 5,000 entries, 5 MiB per file, 50 MiB in total.
var DefaultLimits = Limits{MaxEntries: 5000, MaxFileSize: 5 << 20, MaxTotal: 50 << 20}

// Extract unpacks a (possibly gzipped) tar stream into dest, which must be empty or absent. With stripTop the single
// top-level directory that GitHub and GitLab wrap around an archive is removed. Only regular files and directories
// are created; every other entry, and every unsafe name, fails the whole extraction.
func Extract(r io.Reader, dest string, stripTop bool, lim Limits) error {
	if lim.MaxEntries == 0 {
		lim = DefaultLimits
	}
	br := &peekReader{r: r}
	var tr *tar.Reader
	if b, err := br.peek(2); err == nil && b[0] == 0x1f && b[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("source: not a valid archive: %w", err)
		}
		defer gz.Close()
		tr = tar.NewReader(gz)
	} else {
		tr = tar.NewReader(br)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if ents, err := os.ReadDir(dest); err != nil || len(ents) > 0 {
		return errors.New("source: extraction target is not empty")
	}
	seen := map[string]string{} // folded path -> real path
	var total int64
	entries := 0
	top := ""
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("source: corrupt archive: %w", err)
		}
		switch h.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		case tar.TypeReg, tar.TypeDir:
		default:
			return fmt.Errorf("source: %q is a %s; only files and directories are allowed", h.Name, typeName(h.Typeflag))
		}
		entries++
		if entries > lim.MaxEntries {
			return fmt.Errorf("source: the archive has more than %d entries", lim.MaxEntries)
		}
		name := h.Name
		if strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("source: unsafe path %q", h.Name)
		}
		name = strings.TrimSuffix(name, "/")
		if stripTop {
			first, rest, _ := strings.Cut(name, "/")
			if top == "" {
				top = first
			} else if first != top {
				return fmt.Errorf("source: %q is outside the archive's top-level directory %q", h.Name, top)
			}
			name = rest
			if name == "" {
				continue // the top directory itself
			}
		}
		clean := path.Clean(name)
		if clean != name || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("source: unsafe path %q", h.Name)
		}
		for _, el := range strings.Split(clean, "/") {
			if el == ".git" || el == "" || reservedName(el) {
				return fmt.Errorf("source: %q contains a name that cannot be used on every OS (%q)", h.Name, el)
			}
		}
		fold := strings.ToLower(clean)
		if prev, dup := seen[fold]; dup {
			return fmt.Errorf("source: %q and %q differ only by case (they collide on macOS and Windows)", prev, clean)
		}
		seen[fold] = clean
		target := filepath.Join(dest, filepath.FromSlash(clean))
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if h.Size > lim.MaxFileSize {
			return fmt.Errorf("source: %q is larger than %d bytes", h.Name, lim.MaxFileSize)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if h.Mode&0o111 != 0 {
			mode = 0o755
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return fmt.Errorf("source: %q collides with another entry: %w", clean, err)
		}
		n, err := io.Copy(f, io.LimitReader(tr, lim.MaxFileSize+1))
		cerr := f.Close()
		if err != nil {
			return fmt.Errorf("source: corrupt archive: %w", err)
		}
		if cerr != nil {
			return cerr
		}
		if n > lim.MaxFileSize {
			return fmt.Errorf("source: %q is larger than %d bytes", h.Name, lim.MaxFileSize)
		}
		total += n
		if total > lim.MaxTotal {
			return fmt.Errorf("source: the archive unpacks to more than %d bytes", lim.MaxTotal)
		}
		if err := os.Chmod(target, mode); err != nil {
			return err
		}
	}
}

func typeName(t byte) string {
	switch t {
	case tar.TypeSymlink:
		return "symbolic link"
	case tar.TypeLink:
		return "hard link"
	case tar.TypeChar, tar.TypeBlock:
		return "device"
	case tar.TypeFifo:
		return "FIFO"
	}
	return fmt.Sprintf("entry of type %q", string(t))
}

// reservedName reports Windows device names (with or without an extension) and names Windows silently rewrites.
func reservedName(el string) bool {
	if strings.HasSuffix(el, ".") || strings.HasSuffix(el, " ") {
		return true
	}
	base := strings.ToLower(el)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "con", "prn", "aux", "nul":
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '1' && base[3] <= '9'
}

type peekReader struct {
	r   io.Reader
	buf []byte
}

func (p *peekReader) peek(n int) ([]byte, error) {
	for len(p.buf) < n {
		tmp := make([]byte, n-len(p.buf))
		m, err := p.r.Read(tmp)
		p.buf = append(p.buf, tmp[:m]...)
		if err != nil && len(p.buf) < n {
			return p.buf, err
		}
	}
	return p.buf[:n], nil
}

func (p *peekReader) Read(b []byte) (int, error) {
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		return n, nil
	}
	return p.r.Read(b)
}
