// Package mirrortest serves a bucket in memory, for tests of code that
// mirrors to S3. Not for anything but tests: it checks no signature.
package mirrortest

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Bucket is an S3 bucket in memory, path-style at /b/: PUT, GET and
// ListObjectsV2, which is all internal/s3 asks of one.
type Bucket struct {
	mu      sync.Mutex
	Objects map[string][]byte
}

func (f *Bucket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/b/")
	switch {
	case r.Method == http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.Objects[key] = b
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		type content struct {
			Key  string
			Size int64
		}
		var res struct {
			XMLName  xml.Name  `xml:"ListBucketResult"`
			Contents []content `xml:"Contents"`
		}
		var keys []string
		for k := range f.Objects {
			if strings.HasPrefix(k, r.URL.Query().Get("prefix")) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			res.Contents = append(res.Contents, content{k, int64(len(f.Objects[k]))})
		}
		xml.NewEncoder(w).Encode(res)
	case r.Method == http.MethodGet:
		b, ok := f.Objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
			return
		}
		w.Write(b)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// NewServer starts a bucket named "b"; use its URL as the endpoint, path-style.
func NewServer(t testing.TB) (*Bucket, *httptest.Server) {
	b := &Bucket{Objects: map[string][]byte{}}
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	return b, srv
}
