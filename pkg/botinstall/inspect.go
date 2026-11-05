package botinstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/arketov/iterion/pkg/bundle"
)

// readmeMaxBytes caps the size of the README captured by Inspect. The
// marketplace stores and ships this string back over JSON to the studio
// detail panel — capping keeps the registry entry small even when a bundle
// ships a very long doc.
const readmeMaxBytes = 16 * 1024

// Metadata is the bundle description Inspect extracts from a source repo
// without copying anything into a workspace. It is the registry-facing
// shape: enough to render a marketplace card + detail view, but a strict
// subset of bundle.Manifest + bundle.PresetSpec — fields not relevant to a
// registry preview are intentionally omitted.
type Metadata struct {
	Name        string       `json:"name"`
	DisplayName string       `json:"display_name,omitempty"`
	Description string       `json:"description,omitempty"`
	Author      string       `json:"author,omitempty"`
	Version     string       `json:"version,omitempty"`
	Triggers    []string     `json:"triggers,omitempty"`
	Presets     []PresetMeta `json:"presets,omitempty"`
	README      string       `json:"readme,omitempty"`
}

// PresetMeta is the registry-facing slice of bundle.PresetSpec: just the
// fields the marketplace card / detail view needs. The Prompt body and
// Vars map are deliberately dropped — they're only useful at run time and
// would bloat every registry entry.
type PresetMeta struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name,omitempty"`
	Description string   `json:"description,omitempty"`
	Skills      []string `json:"skills,omitempty"`
}

// Inspect resolves Options like Install does — clones a git URL or uses a
// local directory in place, picks a bundle directory, validates it — and
// returns the bundle's metadata WITHOUT writing anything into a workspace
// destination. It powers the hosted marketplace's submit/refresh path:
// the operator points the registry at a repo, Inspect extracts the card
// metadata, and the entry is persisted. Install runs later, when a user
// asks to install that registry entry.
//
// Only Options.Source, Options.Ref and Options.Path are honoured.
// Options.Dest, Options.Name, Options.Force and Options.Workdir are
// ignored (Inspect never copies anything anywhere).
func Inspect(ctx context.Context, opts Options) (*Metadata, error) {
	if strings.TrimSpace(opts.Source) == "" {
		return nil, fmt.Errorf("a git URL or local path is required")
	}
	url, ref := splitSourceRef(opts.Source)
	if opts.Ref != "" {
		ref = opts.Ref
	}

	repoRoot, cleanup, err := resolveRepoRoot(ctx, url, ref)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	botDir, err := selectBundleDir(repoRoot, opts.Path)
	if err != nil {
		return nil, err
	}

	b, err := bundle.OpenDir(botDir)
	if err != nil {
		return nil, fmt.Errorf("not a valid bot bundle at %s: %w", botDir, err)
	}

	md := &Metadata{}
	if b.Manifest != nil {
		md.Name = b.Manifest.Name
		md.DisplayName = b.Manifest.DisplayName
		md.Description = b.Manifest.Description
		md.Author = b.Manifest.Author
		md.Version = b.Manifest.Version
		md.Triggers = append(md.Triggers, b.Manifest.Triggers...)
	}
	if md.Name == "" {
		md.Name = filepath.Base(botDir)
	}

	if b.PresetsDir != "" {
		specs, _ := bundle.LoadPresets(b.PresetsDir)
		for _, ps := range specs {
			md.Presets = append(md.Presets, PresetMeta{
				Name:        ps.Name,
				DisplayName: ps.DisplayName,
				Description: ps.Description,
				Skills:      append([]string(nil), ps.Skills...),
			})
		}
	}

	if rd, err := readREADME(botDir); err == nil && rd != "" {
		md.README = rd
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read README: %w", err)
	}

	return md, nil
}

// readREADME looks for a README at the bundle root (case-tolerant for the
// common variants README.md / readme.md / Readme.md) and returns up to
// readmeMaxBytes of its content. A missing file returns ("", fs.ErrNotExist)
// so the caller can distinguish "no README" from a real I/O error.
func readREADME(botDir string) (string, error) {
	candidates := []string{"README.md", "readme.md", "Readme.md"}
	for _, name := range candidates {
		path := filepath.Join(botDir, name)
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", err
		}
		if info.IsDir() {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		buf := make([]byte, readmeMaxBytes)
		n, err := io.ReadFull(f, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return "", err
		}
		return string(buf[:n]), nil
	}
	return "", fs.ErrNotExist
}
