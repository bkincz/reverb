package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/db"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type Config struct {
	DB           *bun.DB
	Tokens       TokenConfig
	AccessCookieName string
	CookieName   string
	CookieSecure bool
	CookieDomain string
}

var emailRE = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func Register(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "invalid JSON body")
			return
		}

		fields := map[string]string{}
		if !emailRE.MatchString(body.Email) {
			fields["email"] = "must be a valid email address"
		}
		if len(body.Password) < 8 {
			fields["password"] = "must be at least 8 characters"
		}
		if len(fields) > 0 {
			api.FieldError(w, fields)
			return
		}

		existing, err := FindUserByEmail(r.Context(), cfg.DB, body.Email)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not check existing user")
			return
		}
		if existing != nil {
			api.Error(w, http.StatusConflict, api.CodeConflict, "email already registered")
			return
		}

		user, err := CreateUser(r.Context(), cfg.DB, body.Email, body.Password, "viewer")
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not create user")
			return
		}

		accessToken, err := SignAccess(cfg.Tokens, user.ID, user.Email, user.Role)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not sign token")
			return
		}

		issueAccessCookie(cfg, w, accessToken)

		if err := issueRefreshCookie(r.Context(), cfg, w, user.ID); err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not issue refresh token")
			return
		}

		api.JSON(w, http.StatusCreated, map[string]string{
			"access_token": accessToken,
			"token_type":   "Bearer",
		})
	}
}

func Login(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "invalid JSON body")
			return
		}

		user, err := FindUserByEmail(r.Context(), cfg.DB, body.Email)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not look up user")
			return
		}
		if user == nil || CheckPassword(user.PasswordHash, body.Password) != nil {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "invalid credentials")
			return
		}

		accessToken, err := SignAccess(cfg.Tokens, user.ID, user.Email, user.Role)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not sign token")
			return
		}

		issueAccessCookie(cfg, w, accessToken)

		if err := issueRefreshCookie(r.Context(), cfg, w, user.ID); err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not issue refresh token")
			return
		}

		api.JSON(w, http.StatusOK, map[string]string{
			"access_token": accessToken,
			"token_type":   "Bearer",
		})
	}
}

func Refresh(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := cookieName(cfg)
		cookie, err := r.Cookie(name)
		if err != nil {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing refresh token")
			return
		}

		accessToken, _, err := refreshSession(r.Context(), cfg, w, cookie.Value)
		if errors.Is(err, sql.ErrNoRows) {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "invalid refresh token")
			return
		}
		if errors.Is(err, ErrRefreshTokenExpired) {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "refresh token expired")
			return
		}
		if errors.Is(err, ErrRefreshUserNotFound) {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "user not found")
			return
		}
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not refresh session")
			return
		}

		api.JSON(w, http.StatusOK, map[string]string{
			"access_token": accessToken,
			"token_type":   "Bearer",
		})
	}
}

func Logout(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := cookieName(cfg)
		cookie, err := r.Cookie(name)
		if err == nil {
			hashed := HashRefresh(cookie.Value)

			_, _ = cfg.DB.NewDelete().
				Model((*db.RefreshToken)(nil)).
				Where("token_hash = ?", hashed).
				Exec(r.Context())
		}

		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			Domain:   cfg.CookieDomain,
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   cfg.CookieSecure,
			SameSite: http.SameSiteLaxMode,
		})
		clearAccessCookie(cfg, w)

		api.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func Me(cfg Config) http.HandlerFunc {
	authMW := RequireAuth(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := ClaimsFromContext(r.Context())
		if !ok {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing authentication")
			return
		}

		api.JSON(w, http.StatusOK, map[string]string{
			"id":    claims.UserID,
			"email": claims.Email,
			"role":  claims.Role,
		})
	})

	return func(w http.ResponseWriter, r *http.Request) {
		authMW(inner).ServeHTTP(w, r)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func cookieName(cfg Config) string {
	if cfg.CookieName != "" {
		return cfg.CookieName
	}
	return "reverb_refresh"
}

func accessCookieName(cfg Config) string {
	return cfg.AccessCookieName
}

func issueAccessCookie(cfg Config, w http.ResponseWriter, token string) {
	name := accessCookieName(cfg)
	if name == "" {
		return
	}

	ttl := cfg.Tokens.AccessTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}

	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		Domain:   cfg.CookieDomain,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearAccessCookie(cfg Config, w http.ResponseWriter) {
	name := accessCookieName(cfg)
	if name == "" {
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Domain:   cfg.CookieDomain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func issueRefreshCookie(ctx context.Context, cfg Config, w http.ResponseWriter, userID string) error {
	raw, hashed, err := GenerateRefresh()
	if err != nil {
		return err
	}

	ttl := cfg.Tokens.RefreshTTL
	if ttl == 0 {
		ttl = 7 * 24 * time.Hour
	}

	now := time.Now().UTC()
	rt := &db.RefreshToken{
		ID:        uuid.New().String(),
		UserID:    userID,
		TokenHash: hashed,
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
	}
	if _, err = cfg.DB.NewInsert().Model(rt).Exec(ctx); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(cfg),
		Value:    raw,
		Path:     "/",
		Domain:   cfg.CookieDomain,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

var (
	ErrRefreshTokenExpired = errors.New("auth: refresh token expired")
	ErrRefreshUserNotFound = errors.New("auth: refresh user not found")
)

func refreshSession(ctx context.Context, cfg Config, w http.ResponseWriter, rawRefresh string) (string, *Session, error) {
	hashed := HashRefresh(rawRefresh)

	rt := new(db.RefreshToken)
	err := cfg.DB.NewSelect().Model(rt).Where("token_hash = ?", hashed).Scan(ctx)
	if err != nil {
		return "", nil, err
	}

	if time.Now().After(rt.ExpiresAt) {
		return "", nil, ErrRefreshTokenExpired
	}

	user, err := FindUserByID(ctx, cfg.DB, rt.UserID)
	if err != nil {
		return "", nil, err
	}
	if user == nil {
		return "", nil, ErrRefreshUserNotFound
	}

	if _, err = cfg.DB.NewDelete().Model(rt).Where("id = ?", rt.ID).Exec(ctx); err != nil {
		return "", nil, err
	}

	accessToken, err := SignAccess(cfg.Tokens, user.ID, user.Email, user.Role)
	if err != nil {
		return "", nil, err
	}

	issueAccessCookie(cfg, w, accessToken)

	if err := issueRefreshCookie(ctx, cfg, w, user.ID); err != nil {
		return "", nil, err
	}

	return accessToken, &Session{
		Claims: &Claims{
			UserID: user.ID,
			Email:  user.Email,
			Role:   user.Role,
		},
		Source:    SessionSourceCookie,
		Refreshed: true,
	}, nil
}
