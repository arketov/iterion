package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/arketov/iterion/pkg/notify"
)

// config is the adapter's runtime configuration, read from env.
type config struct {
	listenAddr  string // adapter HTTP bind, e.g. :8090
	callbackURL string // public URL iterion POSTs completion webhooks to
	actionURL   string // public URL Mattermost POSTs consent clicks to

	iterionBase  string // iterion server base URL
	iterionToken string // optional bearer for iterion auth
	clarifyPath  string // path to examples/clarify/main.bot
	model        string // optional model override

	mmHTTPBase  string
	mmWSURL     string
	mmToken     string
	mmBotUserID string

	// webhookSecret authenticates the completion callback. It MUST match
	// ITERION_COMPLETION_WEBHOOK_SECRET on the iterion server: iterion
	// HMAC-signs each payload, this adapter verifies the signature.
	// Empty = no verification (only safe on a trusted private network).
	webhookSecret string
	// mmActionToken authenticates Mattermost consent button clicks. The
	// adapter embeds it in each button's context and checks it on
	// receipt, so a forged POST to /mm/actions is rejected. Empty = no
	// verification.
	mmActionToken string
}

func loadConfig() (config, error) {
	c := config{
		listenAddr:   env("CLARIFY_LISTEN_ADDR", ":8090"),
		callbackURL:  os.Getenv("CLARIFY_CALLBACK_URL"),
		actionURL:    os.Getenv("CLARIFY_ACTION_URL"),
		iterionBase:  env("ITERION_BASE_URL", "http://localhost:8080"),
		iterionToken: os.Getenv("ITERION_AUTH_TOKEN"),
		clarifyPath:  env("CLARIFY_BOT_PATH", "examples/clarify/main.bot"),
		model:        os.Getenv("CLARIFY_MODEL"),
		mmHTTPBase:   os.Getenv("MM_HTTP_BASE"),
		mmWSURL:      os.Getenv("MM_WS_URL"),
		mmToken:      os.Getenv("MM_BOT_TOKEN"),
		mmBotUserID:  os.Getenv("MM_BOT_USER_ID"),

		webhookSecret: os.Getenv("CLARIFY_WEBHOOK_SECRET"),
		mmActionToken: os.Getenv("CLARIFY_MM_ACTION_TOKEN"),
	}
	return c, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	botSource, err := os.ReadFile(cfg.clarifyPath)
	if err != nil {
		log.Fatalf("read clarify bot %q: %v", cfg.clarifyPath, err)
	}

	launchClient := NewLaunchClient(LaunchConfig{
		BaseURL:     cfg.iterionBase,
		AuthToken:   cfg.iterionToken,
		BotSource:   string(botSource),
		CallbackURL: cfg.callbackURL,
		Model:       cfg.model,
	})
	driver := NewMattermostDriver(MattermostConfig{
		HTTPBase:    cfg.mmHTTPBase,
		WSURL:       cfg.mmWSURL,
		Token:       cfg.mmToken,
		BotUserID:   cfg.mmBotUserID,
		ActionURL:   cfg.actionURL,
		ActionToken: cfg.mmActionToken,
	})
	if cfg.webhookSecret == "" {
		log.Println("WARNING: CLARIFY_WEBHOOK_SECRET unset — completion callbacks are NOT authenticated (only safe on a trusted private network)")
	}
	if cfg.mmActionToken == "" {
		log.Println("WARNING: CLARIFY_MM_ACTION_TOKEN unset — consent button clicks are NOT authenticated")
	}
	coord := NewCoordinator(driver, newHeuristicFilter(), launchClient)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// HTTP server: completion callback + Mattermost action endpoint.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /callback", completionHandler(ctx, coord, cfg.webhookSecret))
	mux.HandleFunc("POST /mm/actions", actionHandler(coord, cfg.mmActionToken))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	srv := &http.Server{Addr: cfg.listenAddr, Handler: mux}
	go func() {
		log.Printf("mattermost-clarify: HTTP listening on %s", cfg.listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	// Drive the Mattermost WS loop.
	go func() {
		if err := coord.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("coordinator stopped: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("mattermost-clarify: shutting down")
	_ = srv.Shutdown(context.Background())
}

// completionHandler receives iterion's run-completion webhook and posts
// the final answer back into the originating thread. When secret is set,
// the request's X-Iterion-Signature is verified against the raw body
// (HMAC-SHA256) before the payload is trusted — a forged POST to the
// callback URL is rejected with 401. When secret is empty, verification
// is skipped (documented as private-network-only).
func completionHandler(ctx context.Context, coord *Coordinator, secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Read the raw body once: the signature is computed over these
		// exact bytes, and the payload is decoded from the same buffer.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		if secret != "" && !notify.Verify(secret, body, r.Header.Get(notify.SignatureHeader)) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		var payload notify.CompletionPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		if err := coord.HandleCompletion(ctx, payload); err != nil {
			log.Printf("completion for run %s: %v", payload.RunID, err)
		}
		w.WriteHeader(http.StatusOK)
	}
}

// mmAction is the Mattermost interactive-action request body.
type mmAction struct {
	UserID  string `json:"user_id"`
	Context struct {
		Action    string `json:"action"`
		Granted   bool   `json:"granted"`
		UserID    string `json:"user_id"`
		ChannelID string `json:"channel_id"`
		RootID    string `json:"root_id"`
		// Token is the shared secret the adapter embedded in the button
		// when it posted the consent prompt. Mattermost echoes the
		// context back verbatim, so checking it here authenticates that
		// the click came from a button THIS adapter issued.
		Token string `json:"token"`
	} `json:"context"`
}

// actionHandler translates a Mattermost consent button click into a
// ConsentAction and acknowledges it. When token is set, the click's
// embedded context token must match (constant-time) or the request is
// rejected — this stops a forged POST to /mm/actions from flipping a
// user's consent.
func actionHandler(coord *Coordinator, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var a mmAction
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			http.Error(w, "bad action", http.StatusBadRequest)
			return
		}
		if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.Context.Token)) != 1 {
			http.Error(w, "invalid action token", http.StatusUnauthorized)
			return
		}
		if a.Context.Action == "consent" {
			coord.HandleConsent(ConsentAction{
				Thread:  ThreadRef{ChannelID: a.Context.ChannelID, RootID: a.Context.RootID},
				UserID:  a.Context.UserID,
				Granted: a.Context.Granted,
			})
		}
		// Update the ephemeral prompt so the user sees their choice stuck.
		msg := "✅ Thanks — your messages in this thread will be sent to @clarify-bot."
		if !a.Context.Granted {
			msg = "🚫 Understood — your messages in this thread will NOT be sent."
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ephemeral_text": msg})
	}
}
