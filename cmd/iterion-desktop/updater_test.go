//go:build desktop

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// upstreamUpdaterPublicKeyHex is the SocialGouv/iterion updater key, inherited
// from upstream commit e2da0f313. Its private half belongs to upstream, so
// trusting it makes this build accept upstream releases as updates.
const upstreamUpdaterPublicKeyHex = "f267b9edc0346eddb50478be1cf5f33c5edd67d5c7bac8860a085d9d0e437684"

// TestUpdaterPublicKey_NotUpstream guards the trust anchor of the update
// channel: the embedded key must never be upstream's. Empty means "no update
// channel"; a non-empty key must be a real 32-byte key.
func TestUpdaterPublicKey_NotUpstream(t *testing.T) {
	if updaterPublicKeyHex == upstreamUpdaterPublicKeyHex {
		t.Fatal("updaterPublicKeyHex is the upstream SocialGouv key: this build would accept upstream releases as updates")
	}
	if updaterPublicKeyHex == "" {
		return
	}
	pk, err := hex.DecodeString(updaterPublicKeyHex)
	if err != nil {
		t.Fatalf("updater public key is not valid hex: %v", err)
	}
	if len(pk) != ed25519.PublicKeySize {
		t.Fatalf("updater public key length = %d bytes, want %d", len(pk), ed25519.PublicKeySize)
	}
	var allZero = true
	for _, b := range pk {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("updater public key must not be the all-zero placeholder")
	}
}

// TestCheckForUpdate_RefusesUpstreamSignedManifest serves SocialGouv's real
// signed v4.6.0 manifest and requires that this build does not offer it.
func TestCheckForUpdate_RefusesUpstreamSignedManifest(t *testing.T) {
	dir := filepath.Join("testdata", "upstream-v4.6.0")
	manifestBytes, err := os.ReadFile(filepath.Join(dir, "iterion-desktop-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	sigBytes, err := os.ReadFile(filepath.Join(dir, "iterion-desktop-manifest.json.sig"))
	if err != nil {
		t.Fatal(err)
	}
	upstreamKey, err := hex.DecodeString(upstreamUpdaterPublicKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := hex.DecodeString(strings.TrimSpace(string(sigBytes)))
	if err != nil {
		t.Fatalf("fixture signature is not hex: %v", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(upstreamKey), manifestBytes, sig) {
		t.Fatal("fixture is not a valid upstream-signed manifest; the test would be hollow")
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.HasSuffix(r.URL.Path, ".sig") {
			w.Write(sigBytes)
			return
		}
		w.Write(manifestBytes)
	}))
	defer srv.Close()
	t.Setenv("ITERION_UPDATE_MANIFEST_URL", srv.URL+"/iterion-desktop-manifest.json")

	u := NewUpdater(NewConfig())
	u.client.Timeout = 2 * time.Second

	rel, err := u.CheckForUpdate(context.Background(), ChannelStable)
	if rel != nil {
		t.Fatalf("CheckForUpdate offered an upstream-signed release %+v", rel)
	}
	if err == nil {
		t.Fatal("CheckForUpdate returned no error for an upstream-signed manifest, want a refusal")
	}
	if updaterPublicKeyHex == "" && hits.Load() != 0 {
		t.Fatalf("no key is configured but the updater made %d request(s)", hits.Load())
	}
}

// TestDownloadAndApply_NoKeyFetchesNothing ensures an updater without a key
// refuses before downloading (and never reaches ed25519.Verify, which panics
// on a wrong-sized key).
func TestDownloadAndApply_NoKeyFetchesNothing(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte("artefact"))
	}))
	defer srv.Close()

	u := NewUpdater(NewConfig())
	u.pubkey = nil
	u.client.Timeout = 2 * time.Second

	err := u.DownloadAndApply(context.Background(), &Release{URL: srv.URL + "/a", SHA256: "00", Ed25519: "00"}, nil)
	if err == nil {
		t.Fatal("DownloadAndApply succeeded without a public key")
	}
	if hits.Load() != 0 {
		t.Fatalf("DownloadAndApply fetched the artefact %d time(s) without a public key", hits.Load())
	}
}

// TestCheckForUpdate_404IsNoUpdate ensures the auto-updater treats a 404
// on the manifest URL as "no release published yet" rather than a hard
// error. The desktop UI's "Check for updates" button calls this directly
// and would otherwise surface a confusing "404 Not Found" string to users
// who simply haven't shipped a desktop release on their fork.
func TestCheckForUpdate_404IsNoUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer srv.Close()

	t.Setenv("ITERION_UPDATE_MANIFEST_URL", srv.URL+"/manifest.json")

	cfg := NewConfig()
	u := NewUpdater(cfg)
	// A build without an embedded key refuses before the network; inject one
	// so the 404 path stays covered.
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	u.pubkey = pub
	u.client.Timeout = 2 * time.Second

	rel, err := u.CheckForUpdate(context.Background(), ChannelStable)
	if err != nil {
		t.Fatalf("CheckForUpdate returned error %v on 404, want nil (no update available)", err)
	}
	if rel != nil {
		t.Fatalf("CheckForUpdate returned %+v on 404, want nil", rel)
	}
}
