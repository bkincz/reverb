package auth_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/extra/bundebug"
	"modernc.org/sqlite"

	"github.com/bkincz/reverb/auth"
	"github.com/bkincz/reverb/db"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestDB(t *testing.T) *bun.DB {
	t.Helper()
	sql.Register("sqlite3_test_"+t.Name(), &sqlite.Driver{})
	sqlDB, err := sql.Open("sqlite3_test_"+t.Name(), "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	bunDB := bun.NewDB(sqlDB, sqlitedialect.New())
	bunDB.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(false)))

	ctx := context.Background()
	models := []any{
		(*db.User)(nil),
		(*db.RefreshToken)(nil),
	}
	for _, m := range models {
		if _, err := bunDB.NewCreateTable().Model(m).IfNotExists().Exec(ctx); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	t.Cleanup(func() { _ = bunDB.Close() })
	return bunDB
}

func newTestConfig(t *testing.T) auth.Config {
	t.Helper()
	return auth.Config{
		DB: newTestDB(t),
		Tokens: auth.TokenConfig{
			Secret:     "test-secret-key-at-least-32-bytes!!",
			AccessTTL:  15 * time.Minute,
			RefreshTTL: 7 * 24 * time.Hour,
		},
		CookieName:   "reverb_refresh",
		CookieSecure: false,
	}
}

func post(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func postWithCookie(handler http.Handler, path, body, cookieName, cookieVal string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieVal})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func getWithBearer(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func extractCookie(rr *httptest.ResponseRecorder, name string) string {
	for _, c := range rr.Result().Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func extractAccessToken(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var payload map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return payload["access_token"]
}

// ---------------------------------------------------------------------------
// Register
// ---------------------------------------------------------------------------

func TestRegister_Success(t *testing.T) {
	cfg := newTestConfig(t)
	rr := post(auth.Register(cfg), "/_reverb/auth/register", `{"email":"alice@example.com","password":"supersecret"}`)

	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d — body: %s", rr.Code, rr.Body)
	}

	token := extractAccessToken(t, rr)
	if token == "" {
		t.Fatal("expected non-empty access_token")
	}

	cookie := extractCookie(rr, "reverb_refresh")
	if cookie == "" {
		t.Fatal("expected refresh cookie to be set")
	}
}

func TestRegister_InvalidEmail(t *testing.T) {
	cfg := newTestConfig(t)
	rr := post(auth.Register(cfg), "/_reverb/auth/register", `{"email":"not-an-email","password":"supersecret"}`)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", rr.Code)
	}
}

func TestRegister_ShortPassword(t *testing.T) {
	cfg := newTestConfig(t)
	rr := post(auth.Register(cfg), "/_reverb/auth/register", `{"email":"b@b.com","password":"short"}`)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", rr.Code)
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	cfg := newTestConfig(t)
	post(auth.Register(cfg), "/_reverb/auth/register", `{"email":"dupe@example.com","password":"supersecret"}`)
	rr := post(auth.Register(cfg), "/_reverb/auth/register", `{"email":"dupe@example.com","password":"supersecret"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Login
// ---------------------------------------------------------------------------

func TestLogin_Success(t *testing.T) {
	cfg := newTestConfig(t)
	post(auth.Register(cfg), "/_reverb/auth/register", `{"email":"login@example.com","password":"password123"}`)

	rr := post(auth.Login(cfg), "/_reverb/auth/login", `{"email":"login@example.com","password":"password123"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rr.Code, rr.Body)
	}
	if extractAccessToken(t, rr) == "" {
		t.Fatal("expected access_token")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	cfg := newTestConfig(t)
	post(auth.Register(cfg), "/_reverb/auth/register", `{"email":"pw@example.com","password":"correcthorse"}`)

	rr := post(auth.Login(cfg), "/_reverb/auth/login", `{"email":"pw@example.com","password":"wrongpassword"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestLogin_UnknownEmail(t *testing.T) {
	cfg := newTestConfig(t)
	rr := post(auth.Login(cfg), "/_reverb/auth/login", `{"email":"ghost@example.com","password":"doesntmatter"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

// Both wrong-password and unknown-email must return the same error message to
// prevent account enumeration.
func TestLogin_SameMessageForWrongPasswordAndUnknownEmail(t *testing.T) {
	cfg := newTestConfig(t)
	post(auth.Register(cfg), "/_reverb/auth/register", `{"email":"real@example.com","password":"correcthorse"}`)

	rrWrong := post(auth.Login(cfg), "/_reverb/auth/login", `{"email":"real@example.com","password":"wrong"}`)
	rrGhost := post(auth.Login(cfg), "/_reverb/auth/login", `{"email":"ghost@example.com","password":"anything"}`)

	var bodyWrong, bodyGhost map[string]any
	_ = json.NewDecoder(rrWrong.Body).Decode(&bodyWrong)
	_ = json.NewDecoder(rrGhost.Body).Decode(&bodyGhost)

	msgWrong := bodyWrong["error"].(map[string]any)["message"]
	msgGhost := bodyGhost["error"].(map[string]any)["message"]

	if msgWrong != msgGhost {
		t.Fatalf("messages differ: %q vs %q", msgWrong, msgGhost)
	}
}

// ---------------------------------------------------------------------------
// Refresh
// ---------------------------------------------------------------------------

func TestRefresh_Rotation(t *testing.T) {
	cfg := newTestConfig(t)
	regRR := post(auth.Register(cfg), "/_reverb/auth/register", `{"email":"refresh@example.com","password":"password123"}`)
	if regRR.Code != http.StatusCreated {
		t.Fatalf("register failed: %d — %s", regRR.Code, regRR.Body)
	}

	oldCookie := extractCookie(regRR, "reverb_refresh")
	if oldCookie == "" {
		t.Fatal("no refresh cookie after register")
	}

	rrRefresh := postWithCookie(auth.Refresh(cfg), "/_reverb/auth/refresh", "", "reverb_refresh", oldCookie)
	if rrRefresh.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rrRefresh.Code, rrRefresh.Body)
	}

	newCookie := extractCookie(rrRefresh, "reverb_refresh")
	if newCookie == "" {
		t.Fatal("no new refresh cookie after rotation")
	}
	if newCookie == oldCookie {
		t.Fatal("refresh token was not rotated")
	}

	// Old token must no longer be valid.
	rrReuse := postWithCookie(auth.Refresh(cfg), "/_reverb/auth/refresh", "", "reverb_refresh", oldCookie)
	if rrReuse.Code != http.StatusUnauthorized {
		t.Fatalf("reused old token: want 401, got %d", rrReuse.Code)
	}
}

func TestRefresh_MissingCookie(t *testing.T) {
	cfg := newTestConfig(t)
	rr := post(auth.Refresh(cfg), "/_reverb/auth/refresh", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Logout
// ---------------------------------------------------------------------------

func TestLogout_ClearsCookie(t *testing.T) {
	cfg := newTestConfig(t)
	regRR := post(auth.Register(cfg), "/_reverb/auth/register", `{"email":"logout@example.com","password":"password123"}`)
	cookie := extractCookie(regRR, "reverb_refresh")

	rr := postWithCookie(auth.Logout(cfg), "/_reverb/auth/logout", "", "reverb_refresh", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}

	// Cookie should be cleared (MaxAge -1 sets Value to "" in the response).
	for _, c := range rr.Result().Cookies() {
		if c.Name == "reverb_refresh" && c.MaxAge > 0 {
			t.Fatal("cookie was not cleared")
		}
	}
}

func TestLogout_AlwaysOKWithoutCookie(t *testing.T) {
	cfg := newTestConfig(t)
	rr := post(auth.Logout(cfg), "/_reverb/auth/logout", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// RequireAuth
// ---------------------------------------------------------------------------

func TestRequireAuth_ValidToken(t *testing.T) {
	cfg := newTestConfig(t)
	token, err := auth.SignAccess(cfg.Tokens, "user-1", "u@u.com", "viewer")
	if err != nil {
		t.Fatal(err)
	}

	called := false
	handler := auth.RequireAuth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := getWithBearer(handler, "/", token)
	if rr.Code != http.StatusOK || !called {
		t.Fatalf("want 200+handler called, got %d called=%v", rr.Code, called)
	}
}

func TestRequireAuth_NoToken(t *testing.T) {
	cfg := newTestConfig(t)
	handler := auth.RequireAuth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := getWithBearer(handler, "/", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	cfg := newTestConfig(t)
	handler := auth.RequireAuth(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := getWithBearer(handler, "/", "totally.invalid.jwt")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// RequireRole
// ---------------------------------------------------------------------------

func TestRequireRole_Hierarchy(t *testing.T) {
	cfg := newTestConfig(t)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	cases := []struct {
		tokenRole    string
		requiredRole string
		wantStatus   int
	}{
		{"admin", "admin", http.StatusOK},
		{"admin", "editor", http.StatusOK},
		{"admin", "viewer", http.StatusOK},
		{"editor", "admin", http.StatusForbidden},
		{"editor", "editor", http.StatusOK},
		{"editor", "viewer", http.StatusOK},
		{"viewer", "admin", http.StatusForbidden},
		{"viewer", "editor", http.StatusForbidden},
		{"viewer", "viewer", http.StatusOK},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.tokenRole+"_requires_"+tc.requiredRole, func(t *testing.T) {
			token, err := auth.SignAccess(cfg.Tokens, "u", "u@u.com", tc.tokenRole)
			if err != nil {
				t.Fatal(err)
			}
			handler := auth.RequireRole(cfg, tc.requiredRole)(ok)
			rr := getWithBearer(handler, "/", token)
			if rr.Code != tc.wantStatus {
				t.Fatalf("want %d, got %d", tc.wantStatus, rr.Code)
			}
		})
	}
}

func TestRequireRole_InvalidRequiredRoleFailsClosed(t *testing.T) {
	cfg := newTestConfig(t)

	token, err := auth.SignAccess(cfg.Tokens, "u", "u@u.com", "admin")
	if err != nil {
		t.Fatal(err)
	}

	handler := auth.RequireRole(cfg, "admni")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := getWithBearer(handler, "/", token)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Rate limiter
// ---------------------------------------------------------------------------

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	mw := auth.NewRateLimiter(10)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i+1, rr.Code)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	mw := auth.NewRateLimiter(3)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	blocked := false
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "5.6.7.8:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			blocked = true
			if rr.Header().Get("Retry-After") == "" {
				t.Fatal("missing Retry-After header on 429")
			}
			break
		}
	}
	if !blocked {
		t.Fatal("expected at least one 429 response")
	}
}

func TestRateLimiter_DifferentIPsIndependent(t *testing.T) {
	mw := auth.NewRateLimiter(2)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust IP A.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}

	// IP B should still be allowed.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:1"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("IP B should not be blocked; got %d", rr.Code)
	}
}
