package reverb

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bkincz/reverb/api"
	dbmysql "github.com/bkincz/reverb/db/mysql"
	dbpostgres "github.com/bkincz/reverb/db/postgres"
	dbsqlite "github.com/bkincz/reverb/db/sqlite"
)

// FromEnv builds a Config from environment variables. Returns an error if
// required variables are missing or malformed.
//
// Required:
//
//	REVERB_DB_DRIVER   sqlite | postgres | mysql
//	REVERB_DB_DSN      database connection string
//
// Optional:
//
//	REVERB_AUTH_SECRET
//	REVERB_AUTH_ACCESS_TTL    duration string, e.g. "15m"  (default 15m)
//	REVERB_AUTH_REFRESH_TTL   duration string, e.g. "168h" (default 168h)
//	REVERB_AUTH_COOKIE_SECURE true | false
//	REVERB_AUTH_COOKIE_DOMAIN
//	REVERB_CORS_ORIGINS       comma-separated list of origins
//	REVERB_LOG_MODE           dev | prod
func FromEnv() (Config, error) {
	driver := os.Getenv("REVERB_DB_DRIVER")
	dsn := os.Getenv("REVERB_DB_DSN")

	if driver == "" {
		return Config{}, fmt.Errorf("reverb: REVERB_DB_DRIVER is required")
	}
	if dsn == "" {
		return Config{}, fmt.Errorf("reverb: REVERB_DB_DSN is required")
	}

	cfg := Config{}

	switch strings.ToLower(driver) {
	case "sqlite":
		cfg.DB = dbsqlite.New(dsn)
	case "postgres", "postgresql":
		cfg.DB = dbpostgres.New(dsn)
	case "mysql", "mariadb":
		cfg.DB = dbmysql.New(dsn)
	default:
		return Config{}, fmt.Errorf("reverb: unknown REVERB_DB_DRIVER %q (want sqlite, postgres, or mysql)", driver)
	}

	cfg.Auth.Secret = os.Getenv("REVERB_AUTH_SECRET")

	if v := os.Getenv("REVERB_AUTH_ACCESS_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("reverb: REVERB_AUTH_ACCESS_TTL: %w", err)
		}
		cfg.Auth.AccessTTL = d
	}

	if v := os.Getenv("REVERB_AUTH_REFRESH_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("reverb: REVERB_AUTH_REFRESH_TTL: %w", err)
		}
		cfg.Auth.RefreshTTL = d
	}

	if v := os.Getenv("REVERB_AUTH_COOKIE_SECURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("reverb: REVERB_AUTH_COOKIE_SECURE: %w", err)
		}
		cfg.Auth.CookieSecure = b
	}

	cfg.Auth.CookieDomain = os.Getenv("REVERB_AUTH_COOKIE_DOMAIN")

	if v := os.Getenv("REVERB_CORS_ORIGINS"); v != "" {
		origins := strings.Split(v, ",")
		for i, o := range origins {
			origins[i] = strings.TrimSpace(o)
		}
		cfg.CORS = api.DefaultCORSConfig()
		cfg.CORS.AllowedOrigins = origins
	}

	cfg.LogMode = os.Getenv("REVERB_LOG_MODE")

	return cfg, nil
}
