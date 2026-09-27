package rigd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rigfile/rigfile/internal/platform"
)

// Event is one audit record (docs/rigd.md §6). It never carries a header value, a body, a surrogate or a real secret:
// only names and decisions.
type Event struct {
	Time     time.Time `json:"time"`
	Session  string    `json:"session"`
	Server   string    `json:"server"`
	Method   string    `json:"method,omitempty"`
	Host     string    `json:"host"`
	Path     string    `json:"path,omitempty"` // never the query
	Status   int       `json:"status,omitempty"`
	Decision string    `json:"decision"` // allowed | swapped | blocked
	Reason   string    `json:"reason,omitempty"`
	Secrets  []string  `json:"secrets,omitempty"` // secret references swapped or blocked (names only)
}

// Audit receives events.
type Audit interface{ Log(Event) }

// MemAudit keeps events in memory (tests).
type MemAudit struct {
	mu     sync.Mutex
	Events []Event
}

// Log implements Audit.
func (m *MemAudit) Log(e Event) {
	m.mu.Lock()
	m.Events = append(m.Events, e)
	m.mu.Unlock()
}

// Snapshot returns a copy of the events so far.
func (m *MemAudit) Snapshot() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Event(nil), m.Events...)
}

// FileAudit appends JSON lines to a private file and rotates it at MaxBytes, keeping Keep old files.
type FileAudit struct {
	Path     string
	MaxBytes int64
	Keep     int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// Log implements Audit; failures to write are dropped (the broker must not stall a request over a full disk) but counted.
func (a *FileAudit) Log(e Event) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	b = append(b, '\n')
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		if err := os.MkdirAll(filepath.Dir(a.Path), 0o700); err != nil {
			return
		}
		f, err := os.OpenFile(a.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		if st, err := f.Stat(); err == nil {
			a.size = st.Size()
		}
		a.f = f
		_ = platform.RestrictToUser(a.Path) // user-only ACL on Windows; a no-op where mode 0600 already says so
	}
	max := a.MaxBytes
	if max == 0 {
		max = 10 << 20
	}
	if a.size+int64(len(b)) > max {
		a.rotate()
	}
	n, _ := a.f.Write(b)
	a.size += int64(n)
}

func (a *FileAudit) rotate() {
	keep := a.Keep
	if keep == 0 {
		keep = 3
	}
	a.f.Close()
	a.f = nil
	for i := keep - 1; i >= 1; i-- {
		_ = os.Rename(a.Path+"."+itoa(i), a.Path+"."+itoa(i+1))
	}
	_ = os.Rename(a.Path, a.Path+".1")
	f, err := os.OpenFile(a.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		a.f, a.size = f, 0
	}
}

// Close flushes and closes the file.
func (a *FileAudit) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return err
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
