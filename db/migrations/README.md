# Database migrations

Migrations live as paired `.up.sql` / `.down.sql` files using
`golang-migrate/migrate` (v4). Each migration is identified by a
zero-padded numeric prefix and a short snake_case description:

```
000001_create_products_table.up.sql
000001_create_products_table.down.sql
000002_create_users_table.up.sql
000002_create_users_table.down.sql
```

## When migrations run

`db.RunMigrations` is invoked from [db.ConnectDb](../db.go) on every
startup, gated by `cfg.Db.Migrate` (default true). The source is
`file://` rooted at `cfg.Db.MigrationFolder` (default
`db/migrations`).

Failures during `m.Up()` are logged at warn level and the process
continues — the assumption being that an already-up schema is fine.
Migration *application* errors that aren't `migrate.ErrNoChange`
are still surfaced (after ERR-001's `errors.Is` rewrite).

## Adding a migration

1. Pick the next numeric prefix.
2. Write **both** `.up.sql` and `.down.sql`. The `.down.sql` must
   exactly reverse `.up.sql` — `golang-migrate` runs it when
   `db.clean: true` (see below) or on an explicit roll-back.
3. Statements should be idempotent where the dialect allows
   (`CREATE TABLE IF NOT EXISTS`, etc.) so a partially-applied
   migration can be retried.

## `db.clean: true`

Setting `cfg.Db.Clean = true` makes `RunMigrations` execute every
`.down.sql` before the `.up.sql` series. **This deletes all data**
in the configured database. Intended only for local dev resets;
the config description in [config.go](../../config/config.go#L506)
carries the same warning.

## References

- golang-migrate filename convention: <https://github.com/golang-migrate/migrate/blob/master/MIGRATIONS.md>
- File-source driver options: <https://github.com/golang-migrate/migrate/tree/master/source/file>
