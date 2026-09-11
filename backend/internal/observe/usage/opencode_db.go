package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// requiredOpenCodeColumns are the session columns this reader cannot work
// without. opencode adds columns over time; a database missing any of these is
// an unsupported generation rather than a corrupt file.
var requiredOpenCodeColumns = []string{
	"directory", "cost", "tokens_input", "tokens_output",
	"tokens_cache_read", "tokens_cache_write", "model",
}

type openCodeSession struct {
	ID               string
	Directory        string
	Agent            string
	ModelID          string
	ProviderID       string
	ParentID         string
	Cost             float64
	TokensInput      int64
	TokensOutput     int64
	TokensReasoning  int64
	TokensCacheRead  int64
	TokensCacheWrite int64
	SummaryFiles     int64
	SummaryAdditions int64
	SummaryDeletions int64
	TimeCompacting   *int64
}

type openCodePart struct {
	Seq  int64
	Type string
	Data []byte
}

type openCodeDB struct{ db *sql.DB }

// openOpenCodeDB opens opencode's database read-only. The file belongs to
// another process that may be writing it, so immutable=1 is deliberately NOT
// used: it would serve stale pages and ignore the WAL.
//
// The DSN follows this backend's existing modernc.org/sqlite convention (see
// backend/internal/storage/sqlite/db.go): a "file:" URI built from
// url.URL.EscapedPath(), with driver options passed as "_pragma=name(value)"
// query parameters rather than the mattn/go-sqlite3-style "_busy_timeout=" /
// "_txlock=" parameters.
func openOpenCodeDB(path string) (*openCodeDB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("opencode db unavailable: %w", err)
	}
	fileURL := url.URL{Path: path}
	dsn := "file:" + fileURL.EscapedPath() + "?mode=ro&_pragma=busy_timeout(2000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open opencode db: %w", err)
	}
	handle := &openCodeDB{db: db}
	if err := handle.probeSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return handle, nil
}

func (d *openCodeDB) Close() error { return d.db.Close() }

func (d *openCodeDB) probeSchema(ctx context.Context) error {
	rows, err := d.db.QueryContext(ctx, `SELECT name FROM pragma_table_info('session')`)
	if err != nil {
		return fmt.Errorf("probe opencode schema: %w", err)
	}
	defer rows.Close()
	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("probe opencode schema: %w", err)
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("probe opencode schema: %w", err)
	}
	for _, column := range requiredOpenCodeColumns {
		if !present[column] {
			return fmt.Errorf("opencode schema missing column %q", column)
		}
	}
	return nil
}

const openCodeSessionQuery = `
SELECT id, directory, COALESCE(agent, ''), COALESCE(model, ''), COALESCE(parent_id, ''),
       cost, tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write,
       COALESCE(summary_files, 0), COALESCE(summary_additions, 0), COALESCE(summary_deletions, 0),
       time_compacting
FROM session WHERE directory = ? ORDER BY time_updated DESC LIMIT 1`

func (d *openCodeDB) sessionByDirectory(ctx context.Context, dir string) (openCodeSession, bool, error) {
	var (
		out      openCodeSession
		modelRaw string
	)
	err := d.db.QueryRowContext(ctx, openCodeSessionQuery, dir).Scan(
		&out.ID, &out.Directory, &out.Agent, &modelRaw, &out.ParentID,
		&out.Cost, &out.TokensInput, &out.TokensOutput, &out.TokensReasoning,
		&out.TokensCacheRead, &out.TokensCacheWrite,
		&out.SummaryFiles, &out.SummaryAdditions, &out.SummaryDeletions,
		&out.TimeCompacting,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return openCodeSession{}, false, nil
		}
		return openCodeSession{}, false, fmt.Errorf("read opencode session: %w", err)
	}
	out.ModelID, out.ProviderID = decodeOpenCodeModel(modelRaw)
	return out, true, nil
}

// decodeOpenCodeModel reads opencode's {"id","providerID","variant"} model
// object. A malformed value yields empty identifiers so the event is stored
// unattributed rather than priced against a guess.
func decodeOpenCodeModel(raw string) (modelID, providerID string) {
	if strings.TrimSpace(raw) == "" {
		return "", ""
	}
	var decoded struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return "", ""
	}
	return decoded.ID, decoded.ProviderID
}

const openCodePartsQuery = `
SELECT m.seq, p.data
FROM part p JOIN session_message m ON m.id = p.message_id
WHERE p.session_id = ? AND m.seq > ?
ORDER BY m.seq ASC LIMIT ?`

// partsAfter returns parts ordered by message sequence, plus the highest
// sequence read. A caller that commits then stores the returned sequence can
// resume exactly where it stopped.
func (d *openCodeDB) partsAfter(
	ctx context.Context, sessionID string, afterSeq int64, limit int,
) ([]openCodePart, int64, error) {
	rows, err := d.db.QueryContext(ctx, openCodePartsQuery, sessionID, afterSeq, limit)
	if err != nil {
		return nil, afterSeq, fmt.Errorf("read opencode parts: %w", err)
	}
	defer rows.Close()
	out := make([]openCodePart, 0, limit)
	next := afterSeq
	for rows.Next() {
		var (
			seq  int64
			data []byte
		)
		if err := rows.Scan(&seq, &data); err != nil {
			return nil, afterSeq, fmt.Errorf("read opencode parts: %w", err)
		}
		var typed struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(data, &typed)
		out = append(out, openCodePart{Data: data, Seq: seq, Type: typed.Type})
		if seq > next {
			next = seq
		}
	}
	if err := rows.Err(); err != nil {
		return nil, afterSeq, fmt.Errorf("read opencode parts: %w", err)
	}
	return out, next, nil
}
