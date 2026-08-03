package metric

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	digestReencodeStateKey  = "legacy_tdigest_v1"
	digestReencodeRunning   = "running"
	digestReencodeComplete  = "complete"
	digestReencodeBatchSize = 256
)

type digestReencodeState struct {
	phase      string
	upperRowID int64
	cursor     int64
}

// reencodeLegacyDigests performs the one-time historical conversion for
// SQLite. The rowid upper bound makes the pass finite, while the cursor is
// committed with each batch so an interrupted process resumes safely.
func (s *Store) reencodeLegacyDigests(ctx context.Context) error {
	if s.cfg.Driver != DriverSQLite {
		return nil
	}
	if err := s.ensureDigestReencodeStateTable(ctx); err != nil {
		return err
	}
	state, err := s.loadDigestReencodeState(ctx)
	if err != nil {
		return err
	}
	if state.phase == digestReencodeComplete {
		return nil
	}
	if state.phase == "" {
		if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COALESCE(MAX(rowid), 0) FROM %s", s.tables.rollups)).Scan(&state.upperRowID); err != nil {
			return fmt.Errorf("metric: find legacy digest scan bound: %w", err)
		}
		state.phase = digestReencodeRunning
		if err := s.saveDigestReencodeState(ctx, state); err != nil {
			return err
		}
	}
	for state.cursor < state.upperRowID {
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
			"SELECT rowid, digest FROM %s WHERE rowid > ? AND rowid <= ? AND digest IS NOT NULL AND substr(digest, 1, 2) = ? ORDER BY rowid LIMIT ?",
			s.tables.rollups), state.cursor, state.upperRowID, []byte{tdigestMagic0, tdigestMagic1}, digestReencodeBatchSize)
		if err != nil {
			return fmt.Errorf("metric: scan legacy t-digests: %w", err)
		}
		type item struct {
			rowID  int64
			digest []byte
		}
		batch := make([]item, 0, digestReencodeBatchSize)
		for rows.Next() {
			var row item
			if err := rows.Scan(&row.rowID, &row.digest); err != nil {
				_ = rows.Close()
				return fmt.Errorf("metric: scan legacy t-digest row: %w", err)
			}
			batch = append(batch, row)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("metric: scan legacy t-digest rows: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("metric: close legacy t-digest scan: %w", err)
		}
		if len(batch) == 0 {
			state.cursor = state.upperRowID
			state.phase = digestReencodeComplete
			return s.saveDigestReencodeState(ctx, state)
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("metric: begin legacy t-digest batch: %w", err)
		}
		lastRowID := state.cursor
		for _, row := range batch {
			lastRowID = row.rowID
			digest, err := DecodeTDigest(row.digest)
			if err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("metric: decode legacy t-digest row %d: %w", row.rowID, err)
			}
			encoded := encodeStoredTDigest(digest)
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET digest = ? WHERE rowid = ?", s.tables.rollups), encoded, row.rowID); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("metric: rewrite legacy t-digest row %d: %w", row.rowID, err)
			}
		}
		state.cursor = lastRowID
		if state.cursor >= state.upperRowID {
			state.phase = digestReencodeComplete
		}
		if err := saveDigestReencodeStateTx(ctx, tx, s.tables.state, state); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("metric: commit legacy t-digest batch: %w", err)
		}
	}
	state.phase = digestReencodeComplete
	return s.saveDigestReencodeState(ctx, state)
}

func (s *Store) ensureDigestReencodeStateTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		state_key TEXT PRIMARY KEY, phase TEXT NOT NULL,
		upper_rowid INTEGER NOT NULL, cursor_rowid INTEGER NOT NULL,
		updated_at_milli INTEGER NOT NULL
	)`, s.tables.state))
	if err != nil {
		return fmt.Errorf("metric: create maintenance state table: %w", err)
	}
	return nil
}

func (s *Store) loadDigestReencodeState(ctx context.Context) (digestReencodeState, error) {
	var state digestReencodeState
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT phase, upper_rowid, cursor_rowid FROM %s WHERE state_key = ?", s.tables.state), digestReencodeStateKey).
		Scan(&state.phase, &state.upperRowID, &state.cursor)
	if err == sql.ErrNoRows {
		return digestReencodeState{}, nil
	}
	if err != nil {
		return state, fmt.Errorf("metric: load maintenance state: %w", err)
	}
	return state, nil
}

func (s *Store) saveDigestReencodeState(ctx context.Context, state digestReencodeState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metric: begin maintenance state update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveDigestReencodeStateTx(ctx, tx, s.tables.state, state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metric: commit maintenance state update: %w", err)
	}
	return nil
}

func saveDigestReencodeStateTx(ctx context.Context, tx *sql.Tx, table string, state digestReencodeState) error {
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s
		(state_key, phase, upper_rowid, cursor_rowid, updated_at_milli)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(state_key) DO UPDATE SET phase=excluded.phase,
		upper_rowid=excluded.upper_rowid, cursor_rowid=excluded.cursor_rowid,
		updated_at_milli=excluded.updated_at_milli`, table),
		digestReencodeStateKey, state.phase, state.upperRowID, state.cursor, time.Now().UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("metric: save maintenance state: %w", err)
	}
	return nil
}
