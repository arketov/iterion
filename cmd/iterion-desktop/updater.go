//go:build desktop

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	goruntime "runtime"
	"strings"
	"time"
)

// updaterPublicKeyHex is the Ed25519 verification key used to authenticate
// the release manifest and its artefacts.
//
// It is empty on purpose: the key inherited from upstream verifies
// SocialGouv/iterion releases and we do not hold its private half, so
// trusting it would let an upstream build replace ours. With no key every
// check is refused ("updater: public key not configured") and nothing is
// downloaded. The upstream key must never be restored.
//
// To open our own update channel: generate a pair with
// `scripts/desktop/ed25519-keygen.sh`, store the private half as the
// UPDATER_ED25519_PRIVATE secret of this repository, paste the public hex
// here, and point updateManifestURL and scripts/desktop/generate-manifest.sh
// (RELEASE_BASE_URL, release_notes_url) at this repository.
const updaterPublicKeyHex = ""

// updateManifestURL is the GitHub Releases location of the desktop-only
// manifest. Channel-aware: stable uses /releases/latest/download, prerelease
// would walk /releases via the API.
//
// Override at runtime with ITERION_UPDATE_MANIFEST_URL for local testing
// (lets you serve a custom manifest with `python -m http.server`).
const updateManifestURL = "https://github.com/SocialGouv/iterion/releases/latest/download/iterion-desktop-manifest.json"

// Release describes a single available update.
type Release struct {
	Version         string `json:"version"`
	URL             string `json:"url"`
	Size            int64  `json:"size"`
	SHA256          string `json:"sha256"`
	Ed25519         string `json:"ed25519"`
	ReleaseNotesURL string `json:"release_notes_url"`
	ReleasedAt      string `json:"released_at"`
}

// manifest is the on-disk JSON shape published with each release.
type manifest struct {
	Version         string                 `json:"version"`
	ReleasedAt      string                 `json:"released_at"`
	Channel         string                 `json:"channel"`
	Artifacts       map[string]artefactRef `json:"artifacts"`
	ReleaseNotesURL string                 `json:"release_notes_url"`
}

type artefactRef struct {
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Ed25519 string `json:"ed25519"`
}

// Updater is the desktop's auto-update client.
type Updater struct {
	cfg    *Config
	pubkey ed25519.PublicKey
	client *http.Client
}

// NewUpdater constructs an Updater. Returns a usable instance even if the
// public key is not embedded — it then refuses every check.
func NewUpdater(cfg *Config) *Updater {
	pk, _ := hex.DecodeString(updaterPublicKeyHex)
	return &Updater{
		cfg:    cfg,
		pubkey: ed25519.PublicKey(pk),
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// errManifestNotPublished signals "the channel exists but no release has
// been published yet" — i.e. GitHub Releases returns 404 on the manifest
// URL. The caller treats it as "no update available" rather than a
// hard error so the UI's manual "Check for updates" button can return
// silently instead of surfacing a confusing 404 to the user.
var errManifestNotPublished = errors.New("updater: manifest not yet published")

// CheckForUpdate fetches the manifest, verifies its signature, and returns
// the matching artefact if its version is greater than the running version.
// Returns (nil, nil) when no update is available — including the case
// where the channel has no release yet (manifest 404 from GitHub).
func (u *Updater) CheckForUpdate(ctx context.Context, channel string) (*Release, error) {
	if len(u.pubkey) != ed25519.PublicKeySize {
		return nil, errors.New("updater: public key not configured")
	}
	if channel == "" {
		channel = ChannelStable
	}
	url := manifestURL(channel)
	body, sig, err := u.fetchManifestAndSig(ctx, url)
	if errors.Is(err, errManifestNotPublished) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(u.pubkey, body, sig) {
		return nil, errors.New("updater: manifest signature invalid")
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		// Surface the first chunk of the body so a misformed manifest
		// (e.g. v0.8.0's stray newline inside an Ed25519 hex string)
		// is debuggable from the user-facing error message.
		snippet := string(body)
		const max = 200
		if len(snippet) > max {
			snippet = snippet[:max] + "…"
		}
		return nil, fmt.Errorf("updater: parse manifest (%d bytes from %s): %w; body[:%d]=%q", len(body), url, err, max, snippet)
	}
	if !versionGreater(m.Version, currentVersion()) {
		return nil, nil
	}
	plat := goruntime.GOOS + "/" + goruntime.GOARCH
	a, ok := m.Artifacts[plat]
	if !ok {
		return nil, fmt.Errorf("updater: no artefact for platform %q", plat)
	}
	return &Release{
		Version:         m.Version,
		URL:             a.URL,
		Size:            a.Size,
		SHA256:          a.SHA256,
		Ed25519:         a.Ed25519,
		ReleaseNotesURL: m.ReleaseNotesURL,
		ReleasedAt:      m.ReleasedAt,
	}, nil
}

// DownloadAndApply fetches the artefact, verifies its signature & SHA256,
// and swaps the running binary in place. The exact swap mechanics live in
// per-platform helpers (apply_*).
func (u *Updater) DownloadAndApply(ctx context.Context, rel *Release, progress func(float64)) error {
	if rel == nil {
		return errors.New("updater: nil release")
	}
	if len(u.pubkey) != ed25519.PublicKeySize {
		return errors.New("updater: public key not configured")
	}
	body, err := u.fetchArtifact(ctx, rel.URL, rel.Size, progress)
	if err != nil {
		return err
	}
	// Verify SHA256 first (cheap, catches accidental corruption).
	gotHash := sha256.Sum256(body)
	if hex.EncodeToString(gotHash[:]) != rel.SHA256 {
		return errors.New("updater: artefact sha256 mismatch")
	}
	// Then Ed25519 — this is the trust anchor.
	sig, err := hex.DecodeString(rel.Ed25519)
	if err != nil {
		return fmt.Errorf("updater: invalid signature hex: %w", err)
	}
	if !ed25519.Verify(u.pubkey, body, sig) {
		return errors.New("updater: artefact signature invalid")
	}
	return applyArtifact(body, rel)
}

func (u *Updater) fetchManifestAndSig(ctx context.Context, url string) ([]byte, []byte, error) {
	body, err := u.httpGet(ctx, url)
	if err != nil {
		return nil, nil, err
	}
	sigHex, err := u.httpGet(ctx, url+".sig")
	if err != nil {
		return nil, nil, err
	}
	sig, err := hex.DecodeString(strings.TrimSpace(string(sigHex)))
	if err != nil {
		return nil, nil, fmt.Errorf("updater: invalid sig hex: %w", err)
	}
	return body, sig, nil
}

// maxManifestBytes caps the manifest + signature reads. Both are tiny
// (a JSON blob and a hex signature); the cap only guards against a
// hostile/misconfigured endpoint returning an oversized body.
const maxManifestBytes = 4 << 20 // 4 MiB

func (u *Updater) fetchArtifact(ctx context.Context, url string, size int64, progress func(float64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("updater: GET %s: %s", url, resp.Status)
	}
	// We could stream; for v1 a buffered read is simpler and lets us
	// hash + verify before touching the disk. Bound the read by the
	// manifest-declared size so a compromised CDN / MITM can't OOM the
	// process with a multi-GiB body — the Ed25519 verification runs AFTER
	// this read, so it can't protect the allocation.
	if progress != nil {
		progress(0)
	}
	var reader io.Reader = resp.Body
	if size > 0 {
		reader = io.LimitReader(resp.Body, size+1) // +1 to detect an oversize body
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if size > 0 && int64(len(body)) > size {
		return nil, fmt.Errorf("updater: artifact at %s exceeds manifest size %d", url, size)
	}
	if progress != nil {
		progress(1)
	}
	return body, nil
}

func (u *Updater) httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// 404 on the manifest URL typically means no desktop release has been
	// published yet (the /releases/latest/download/<file> endpoint returns
	// 404 when the file is missing on the latest release, OR when no
	// release exists at all). Bubble up a sentinel so CheckForUpdate can
	// translate it to "no update available" rather than a user-facing
	// error message.
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", errManifestNotPublished, url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("updater: GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes))
}

func manifestURL(channel string) string {
	// Local-testing override.
	if v := readEnv("ITERION_UPDATE_MANIFEST_URL"); v != "" {
		return v
	}
	if channel == ChannelPrerelease {
		// For prereleases, the CI workflow uploads a separate manifest
		// on the latest prerelease tag — a simple path swap.
		return strings.Replace(updateManifestURL, "/latest/", "/latest-prerelease/", 1)
	}
	return updateManifestURL
}
