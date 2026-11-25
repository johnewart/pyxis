---
title: Schema changes ship with an upgrade path and an old-schema test
---
Any change to an llmd persistence model (`internal/store` GORM models) or a knowledge SQL migration must keep existing databases bootable:

- **Additive, nullable/defaulted columns** are fine through `AutoMigrate` alone.
- **Anything else** (new NOT NULL column without a default, primary-key change, rename, drop) needs a numbered step in `runDataMigrations` (or a new `NNN_*.sql` knowledge migration). **Never edit an already-shipped knowledge migration in place**: databases that recorded that version will never re-run it.
- **If a change truly can't be migrated** (pre-1.0 only), `llmdhost.New` must detect the old shape and return `*LegacyDatabaseError` before migrating anything, so the host can offer a backup instead of crashing.
- **Every such change gets a test that starts from the previous schema** (create the old tables plus rows by hand, then open through `llmdhost.New` / `store.AutoMigrate`). `NewTestSQLite` starts empty and cannot catch this class of bug.

Why: the workspace_id → (tenant_id, scope_id) change made every existing dev database crash at startup with "Cannot add a NOT NULL column with default value NULL". All tests still passed because none of them started from an existing database.