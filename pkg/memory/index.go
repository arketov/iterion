package memory

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arketov/iterion/pkg/knowledge"
)

// frontmatterPeekBytes caps how much of each .md file BuildIndex
// reads to extract metadata. Frontmatter + fallback first H1 fit
// in a few hundred bytes; anything past this is body content we
// don't care about for the index. Parsing is shared with the cloud
// adapter via knowledge.ParseMarkdownMeta.
const frontmatterPeekBytes = 4096

// IndexEntry summarises one Markdown file in a scope.
type IndexEntry struct {
	Path        string
	Title       string
	Description string
	Tags        []string
}

// BuildIndex walks the scope recursively, collecting one
// IndexEntry per Markdown file. Lexicographic by path.
func (s *Scope) BuildIndex() ([]IndexEntry, error) {
	if s.root == "" {
		return nil, nil
	}
	if _, err := os.Stat(s.root); os.IsNotExist(err) {
		return nil, nil
	}

	var entries []IndexEntry
	err := filepath.WalkDir(s.root, func(abs string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(abs), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(s.root, abs)
		e, err := readIndexEntry(abs)
		if err != nil {
			return nil
		}
		e.Path = rel
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// readIndexEntry opens abs and reads only the first ~4KB to peek
// at frontmatter + the H1 fallback. Avoids slurping multi-MB
// session files when only the prefix is interesting.
func readIndexEntry(abs string) (IndexEntry, error) {
	f, err := os.Open(abs)
	if err != nil {
		return IndexEntry{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, frontmatterPeekBytes))
	if err != nil {
		return IndexEntry{}, err
	}
	title, desc, tags := knowledge.ParseMarkdownMeta(data)
	return IndexEntry{Title: title, Description: desc, Tags: tags}, nil
}
