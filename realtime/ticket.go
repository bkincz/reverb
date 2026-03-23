package realtime

import (
	"net/http"
	"time"

	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/auth"
)

const ticketTTL = 30 * time.Second

func HandleTicket(authCfg auth.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok || claims == nil {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "authentication required")
			return
		}

		ticket, err := auth.SignSSETicket(authCfg.Tokens, claims.Role, ticketTTL)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not issue ticket")
			return
		}

		api.JSON(w, http.StatusOK, map[string]any{
			"ticket":     ticket,
			"expires_in": int(ticketTTL.Seconds()),
		})
	}
}
