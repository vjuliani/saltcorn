// Package sync implements the versioned offline exchange shared by PostgreSQL
// and SQLite. The caller owns the tenant/actor transaction and must roll back
// when Exchange returns an error. No client-supplied role is trusted.
package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

const ProtocolVersion = 1
const MaxMutations = 100
const MaxSnapshotRows = 2000

var (
	ErrVersion  = errors.New("sync: unsupported protocol")
	ErrScope    = errors.New("sync: scope mismatch")
	ErrInvalid  = errors.New("sync: invalid request")
	ErrSchema   = errors.New("sync: schema changed")
	ErrTooLarge = errors.New("sync: snapshot exceeds limit")
	identifier  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

type Scope struct {
	Tenant string `json:"tenant"`
	Actor  string `json:"actor"`
	Table  string `json:"table"`
}
type Mutation struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	RowID       int            `json:"row_id,omitempty"`
	BaseVersion string         `json:"base_version,omitempty"`
	Values      map[string]any `json:"values,omitempty"`
}
type Request struct {
	Protocol      int        `json:"protocol"`
	Scope         Scope      `json:"scope"`
	ClientID      string     `json:"client_id"`
	SchemaVersion int64      `json:"schema_version"`
	Checkpoint    string     `json:"checkpoint"`
	Mutations     []Mutation `json:"mutations"`
}
type Result struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
	RowID  int    `json:"row_id,omitempty"`
}
type Field struct {
	Name     string             `json:"name"`
	Type     metadata.FieldType `json:"type"`
	Required bool               `json:"required"`
}
type Response struct {
	Protocol      int      `json:"protocol"`
	Scope         Scope    `json:"scope"`
	SchemaVersion int64    `json:"schema_version"`
	Fields        []Field  `json:"fields"`
	Checkpoint    string   `json:"checkpoint"`
	Results       []Result `json:"results"`
	// Full authorized replacement, never a delta. Absence means deleted OR no
	// longer visible. The client keeps unacknowledged drafts separately.
	Rows []map[string]any `json:"rows"`
}

func Validate(req Request, scope Scope) error {
	if req.Protocol != ProtocolVersion {
		return ErrVersion
	}
	if req.Scope != scope || scope.Actor == "" || scope.Tenant == "" || scope.Table == "" {
		return ErrScope
	}
	if !identifier.MatchString(req.ClientID) || len(req.Mutations) > MaxMutations || req.SchemaVersion < 0 || len(req.Checkpoint) > 128 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, m := range req.Mutations {
		if !identifier.MatchString(m.ID) || seen[m.ID] || len(m.BaseVersion) > 128 {
			return ErrInvalid
		}
		seen[m.ID] = true
		switch m.Kind {
		case "create":
			if m.RowID != 0 || m.BaseVersion != "" {
				return ErrInvalid
			}
		case "update", "delete":
			if m.RowID <= 0 || m.BaseVersion == "" {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
		if m.Kind == "delete" && len(m.Values) > 0 {
			return ErrInvalid
		}
	}
	return nil
}

// EnsureSchema runs during tenant provisioning, with the schema owner.
// Exchange never executes DDL so RLS application roles need no ownership.
func EnsureSchema(ctx context.Context, tx database.Tx) error { return outbox.EnsureSchemaTx(ctx, tx) }

func Exchange(ctx context.Context, tx database.Tx, role identity.RoleID, scope Scope, req Request) (*Response, error) {
	if err := Validate(req, scope); err != nil {
		return nil, err
	}
	table, err := metadata.GetTable(ctx, tx, scope.Table)
	if err != nil {
		return nil, err
	}
	if !identity.CanRead(role, table.MinRoleRead) || (len(req.Mutations) > 0 && !identity.CanWrite(role, table.MinRoleWrite)) {
		return nil, records.ErrNotAuthorized
	}
	// Serialize sync requests for this table before reading schema/idempotency.
	// SQLite WithTenant already begins IMMEDIATE. Other writers remain visible
	// through the final SELECT's MVCC snapshot (no timestamp cursor can skip them).
	if tx.Dialect() == database.DialectPostgres {
		if err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(current_schema() || ':sync:' || $1)::bigint)", scope.Table); err != nil {
			return nil, err
		}
	}
	schema, err := metadata.CurrentVersion(ctx, tx)
	if err != nil {
		return nil, err
	}
	if len(req.Mutations) > 0 && req.SchemaVersion != schema {
		return nil, ErrSchema
	}
	fields, err := metadata.ListFields(ctx, tx, table.ID)
	if err != nil {
		return nil, err
	}
	response := &Response{Protocol: ProtocolVersion, Scope: scope, SchemaVersion: schema, Fields: []Field{}, Results: []Result{}}
	for _, f := range fields {
		response.Fields = append(response.Fields, Field{Name: f.Name, Type: f.Type, Required: f.Required})
	}
	for _, m := range req.Mutations {
		// Actor and table are part of the key and payload. A login switch cannot
		// reuse another actor's acknowledgement or apply their pending edits.
		keyData, _ := json.Marshal([]string{"sync-v1", scope.Actor, scope.Table, req.ClientID, m.ID})
		sum := sha256.Sum256(keyData)
		key := "sync-v1:" + hex.EncodeToString(sum[:])
		result, _, err := outbox.DoTx(ctx, tx, key, m, func(ctx context.Context, tx database.Tx) (any, []outbox.Event, error) {
			if err := tx.Exec(ctx, "SAVEPOINT sc_sync_mutation"); err != nil {
				return nil, nil, err
			}
			rowID, applyErr := apply(ctx, tx, role, scope.Table, fields, m)
			if applyErr != nil {
				if err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT sc_sync_mutation"); err != nil {
					return nil, nil, err
				}
			}
			if err := tx.Exec(ctx, "RELEASE SAVEPOINT sc_sync_mutation"); err != nil {
				return nil, nil, err
			}
			r := Result{ID: m.ID, Status: "applied", RowID: rowID}
			if applyErr != nil {
				var pgErr *pgconn.PgError
				if errors.As(applyErr, &pgErr) && pgErr.Code == "42501" {
					return nil, nil, records.ErrNotAuthorized
				}
				r.RowID = m.RowID
				switch {
				case errors.Is(applyErr, records.ErrVersionConflict):
					r.Status = "conflict"
					r.Code = "version_conflict"
				case errors.Is(applyErr, records.ErrRecordNotFound):
					r.Status = "conflict"
					r.Code = "missing_or_inaccessible"
				case errors.Is(applyErr, records.ErrNotAuthorized):
					return nil, nil, applyErr
				case errors.Is(applyErr, records.ErrDuplicateValue):
					r.Status = "conflict"
					r.Code = "duplicate_value"
				case errors.Is(applyErr, records.ErrInvalidReference):
					r.Status = "conflict"
					r.Code = "invalid_reference"
				case errors.Is(applyErr, records.ErrUnknownField), errors.Is(applyErr, records.ErrTypeMismatch), errors.Is(applyErr, records.ErrRequiredField), errors.Is(applyErr, records.ErrNoFields):
					r.Status = "rejected"
					r.Code = "invalid_values"
				default:
					return nil, nil, applyErr
				}
				return r, nil, nil
			}
			return r, []outbox.Event{{Type: "sync." + m.Kind, Payload: map[string]any{"table": scope.Table, "row_id": rowID, "actor": scope.Actor}}}, nil
		})
		if err != nil {
			return nil, err
		}
		// Replay is decoded JSON whereas a fresh result is a struct.
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		var r Result
		if err := json.Unmarshal(encoded, &r); err != nil {
			return nil, err
		}
		response.Results = append(response.Results, r)
	}
	rows, err := records.RowsTx(ctx, tx, role, records.Query{Table: scope.Table, OrderBy: []records.OrderTerm{{Field: "id"}}, Limit: MaxSnapshotRows + 1})
	if err != nil {
		return nil, err
	}
	if len(rows) > MaxSnapshotRows {
		return nil, ErrTooLarge
	}
	response.Rows = rows
	// Checkpoint describes the complete authorized snapshot/schema, not an
	// offset or timestamp. Interrupted exchanges safely repeat mutation IDs.
	payload, err := json.Marshal(struct {
		Scope  Scope
		Schema int64
		Rows   []map[string]any
	}{scope, schema, rows})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(payload)
	response.Checkpoint = hex.EncodeToString(hash[:])
	return response, nil
}

func apply(ctx context.Context, tx database.Tx, role identity.RoleID, table string, fields []metadata.Field, m Mutation) (int, error) {
	values := make(map[string]any, len(m.Values))
	for k, v := range m.Values {
		values[k] = v
	}
	for _, f := range fields {
		value, exists := values[f.Name]
		if !exists || value == nil {
			continue
		}
		switch f.Type {
		case metadata.FieldInteger, metadata.FieldKey:
			switch number := value.(type) {
			case json.Number:
				n, err := strconv.ParseInt(string(number), 10, 32)
				if err != nil {
					return 0, records.ErrTypeMismatch
				}
				values[f.Name] = int(n)
			case float64:
				if number != float64(int32(number)) {
					return 0, records.ErrTypeMismatch
				}
				values[f.Name] = int(number)
			}
		case metadata.FieldFloat:
			if number, ok := value.(json.Number); ok {
				n, err := number.Float64()
				if err != nil {
					return 0, records.ErrTypeMismatch
				}
				values[f.Name] = n
			}
		case metadata.FieldDate:
			if str, ok := value.(string); ok {
				date, err := time.Parse(time.RFC3339Nano, str)
				if err != nil {
					return 0, records.ErrTypeMismatch
				}
				values[f.Name] = date
			}
		}
	}
	switch m.Kind {
	case "create":
		row, err := records.CreateRecordTx(ctx, tx, role, table, values, nil)
		if err != nil {
			return 0, err
		}
		id, err := strconv.Atoi(fmt.Sprint(row["id"]))
		return id, err
	case "update":
		_, err := records.UpdateRecordTx(ctx, tx, role, table, m.RowID, m.BaseVersion, values, nil)
		return m.RowID, err
	case "delete":
		return m.RowID, records.DeleteRecordTx(ctx, tx, role, table, m.RowID, m.BaseVersion, nil)
	}
	return 0, ErrInvalid
}
