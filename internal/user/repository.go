package user

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/internal/platform/cache"
	"github.com/sksmith/go-micro-example/internal/platform/persistence"
)

type dbRepo struct {
	conn  persistence.Conn
	cache cache.Cache
	ttl   time.Duration
}

// NewPostgresRepo returns a user repository that talks to Postgres
// via conn. The cache is opt-in via SetCache (DSN-021c): callers
// that want read-through caching wire a cache.Cache and TTL;
// callers that don't pay no cost.
func NewPostgresRepo(conn persistence.Conn) *dbRepo {
	log.Info().Msg("creating user repository...")
	return &dbRepo{conn: conn}
}

// SetCache installs the optional Redis-backed read-through cache
// (DSN-021c). A short TTL — typically 60s — propagates revocations
// without requiring explicit cache-bust on the user-management
// endpoints. Passing nil disables caching.
func (r *dbRepo) SetCache(c cache.Cache, ttl time.Duration) {
	r.cache = c
	if ttl > 0 {
		r.ttl = ttl
	} else {
		r.ttl = time.Minute
	}
}

// userCacheKey is the per-username cache key. The "v1" suffix is the
// global invalidation lever — bump it when the cached shape changes
// to drop every entry without touching Redis directly.
func userCacheKey(username string) string { return "user:" + username + ":v1" }

func (r *dbRepo) Create(ctx context.Context, u *User, txs ...persistence.UpdateOptions) error {
	m := persistence.StartMetric("Create")
	tx := persistence.GetUpdateOptions(r.conn, txs...)

	_, err := tx.Exec(ctx, `
		INSERT INTO users (username, password, is_admin, created_at)
                      VALUES ($1, $2, $3, $4);`,
		u.Username, u.HashedPassword, u.IsAdmin, u.Created)
	if err != nil {
		m.Complete(err)
		return err
	}
	r.populateCache(ctx, *u)
	m.Complete(nil)
	return nil
}

func (r *dbRepo) Get(ctx context.Context, username string, txs ...persistence.QueryOptions) (User, error) {
	m := persistence.StartMetric("GetUser")
	tx, forUpdate := persistence.GetQueryOptions(r.conn, txs...)

	if r.cache != nil {
		if u, ok, err := cache.Get[User](ctx, r.cache, userCacheKey(username)); err == nil && ok {
			m.Complete(nil)
			return u, nil
		} else if err != nil {
			log.Ctx(ctx).Warn().Err(err).Str("username", username).Msg("user cache get failed; falling through to DB")
		}
	}

	query := `SELECT username, password, is_admin, created_at FROM users WHERE username = $1 ` + forUpdate
	log.Ctx(ctx).Debug().Str("query", query).Str("username", username).Msg("getting user")

	var u User
	err := tx.QueryRow(ctx, query, username).
		Scan(&u.Username, &u.HashedPassword, &u.IsAdmin, &u.Created)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, persistence.ErrNotFound
		}
		return User{}, err
	}

	r.populateCache(ctx, u)
	m.Complete(nil)
	return u, nil
}

func (r *dbRepo) Delete(ctx context.Context, username string, txs ...persistence.UpdateOptions) error {
	m := persistence.StartMetric("DeleteUser")
	tx := persistence.GetUpdateOptions(r.conn, txs...)

	_, err := tx.Exec(ctx, `DELETE FROM users WHERE username = $1`, username)
	if err != nil {
		m.Complete(err)
		if errors.Is(err, pgx.ErrNoRows) {
			return persistence.ErrNotFound
		}
		return err
	}

	r.invalidateCache(ctx, username)
	m.Complete(nil)
	return nil
}

func (r *dbRepo) populateCache(ctx context.Context, u User) {
	if r.cache == nil {
		return
	}
	if err := cache.Set(ctx, r.cache, userCacheKey(u.Username), u, r.ttl); err != nil {
		// Best-effort. The short TTL is the safety net.
		log.Ctx(ctx).Warn().Err(err).Str("username", u.Username).Msg("user cache populate failed; serving DB result")
	}
}

func (r *dbRepo) invalidateCache(ctx context.Context, username string) {
	if r.cache == nil {
		return
	}
	if err := r.cache.Delete(ctx, userCacheKey(username)); err != nil {
		log.Ctx(ctx).Warn().Err(err).Str("username", username).Msg("user cache invalidate failed; TTL will eventually expire stale entry")
	}
}
