package preview

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/collections"
)

// ---------------------------------------------------------------------------
// Token types
// ---------------------------------------------------------------------------

type Claim struct {
	Collection string    `json:"c"`
	EntryID    string    `json:"e"`
	Exp        time.Time `json:"x"`
}

// ---------------------------------------------------------------------------
// Issue / Validate
// ---------------------------------------------------------------------------

func derivedSecret(secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("reverb:preview"))
	return mac.Sum(nil)
}

func Issue(secret, collection, entryID string) (string, error) {
	c := Claim{
		Collection: collection,
		EntryID:    entryID,
		Exp:        time.Now().UTC().Add(10 * time.Minute),
	}

	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("preview: marshal claim: %w", err)
	}

	key := derivedSecret(secret)
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	sig := mac.Sum(nil)

	combined := append(sig, payload...)
	return base64.RawURLEncoding.EncodeToString(combined), nil
}

func Validate(secret, token string) (*Claim, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("preview: decode token: %w", err)
	}
	if len(raw) < 32 {
		return nil, errors.New("preview: token too short")
	}

	sig := raw[:32]
	payload := raw[32:]

	key := derivedSecret(secret)
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	expected := mac.Sum(nil)

	if !hmac.Equal(sig, expected) {
		return nil, errors.New("preview: invalid token signature")
	}

	var c Claim
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("preview: unmarshal claim: %w", err)
	}

	if time.Now().After(c.Exp) {
		return nil, nil
	}

	return &c, nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func HandleIssue(secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Collection string `json:"collection"`
			EntryID    string `json:"entry_id"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "invalid JSON body")
			return
		}
		if body.Collection == "" || body.EntryID == "" {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "collection and entry_id are required")
			return
		}

		token, err := Issue(secret, body.Collection, body.EntryID)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		api.JSON(w, http.StatusOK, map[string]string{"token": token})
	})
}

func HandlePreview(db *bun.DB, reg *collections.Registry, secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing token")
			return
		}

		claim, err := Validate(secret, token)
		if err != nil {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "invalid token")
			return
		}
		if claim == nil {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "token expired")
			return
		}

		e, ok := reg.Get(claim.Collection)
		if !ok {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "collection not found")
			return
		}

		result, err := collections.GetEntry(r.Context(), db, claim.Collection, claim.EntryID, "public", e.Schema(), collections.ReadOptions{})
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}
		if result == nil {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "entry not found")
			return
		}

		api.JSON(w, http.StatusOK, result)
	})
}
