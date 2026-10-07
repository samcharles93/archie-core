package webui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// chatFileTTL is how long a file a turn sent stays downloadable.
const chatFileTTL = time.Hour

// chatFiles serves the files a chat turn sent from the Gateway's host. Only a
// path a turn named is reachable, through an unguessable token that expires;
// no request can name a path of its own.
type chatFiles struct {
	mu    sync.Mutex
	files map[string]chatFile
	now   func() time.Time
}

type chatFile struct {
	path    string
	name    string
	expires time.Time
}

func (c *chatFiles) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// offer registers path and returns the token that downloads it.
func (c *chatFiles) offer(path, name string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	token := hex.EncodeToString(b[:])
	if name == "" {
		name = filepath.Base(path)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.files == nil {
		c.files = make(map[string]chatFile)
	}
	now := c.clock()
	for t, f := range c.files {
		if now.After(f.expires) {
			delete(c.files, t)
		}
	}
	c.files[token] = chatFile{path: path, name: name, expires: now.Add(chatFileTTL)}
	return token
}

func (c *chatFiles) lookup(token string) (chatFile, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.files[token]
	if !ok || c.clock().After(f.expires) {
		return chatFile{}, false
	}
	return f, true
}

func (s *Server) handleChatFile(w http.ResponseWriter, r *http.Request) {
	f, ok := s.chatFiles.lookup(r.PathValue("token"))
	if !ok {
		http.Error(w, "that file is no longer available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, f.path)
}

func chatFileURL(token string) string { return fmt.Sprintf("/api/chat/file/%s", token) }
