package repo

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/store"
)

// auditMask replaces a value whose field name looks like a credential.
const auditMask = "***"

// sensitiveNameParts are the field-name fragments that make a metadata value a
// secret. An audit record is a permanent, operator-visible history, so the rule
// is to drop anything that looks credential-shaped rather than guess.
var sensitiveNameParts = []string{
	"key", "token", "password", "passwd", "secret", "credential",
	"authorization", "bearer", "csrf", "xsrf", "cookie", "signature",
}

// AuditEvent is one recorded administrative action.
type AuditEvent struct {
	ID         int64          `json:"id"`
	ActorID    int64          `json:"actor_id"`
	Action     string         `json:"action"`
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// AuditRepo records who changed what, for the activity view (FR-10).
type AuditRepo struct {
	st   *store.Store
	red  *logging.Redactor
	mask string
}

// NewAuditRepo builds an audit repository. Redaction runs before anything is
// written, so a recorded event can never become the place a secret leaks.
func NewAuditRepo(st *store.Store) *AuditRepo {
	return &AuditRepo{st: st, red: logging.NewRedactor(), mask: auditMask}
}

// WithRedactor shares the application redactor so live stream keys registered
// with the logger are masked in audit records too.
func (r *AuditRepo) WithRedactor(red *logging.Redactor) *AuditRepo {
	if red != nil {
		r.red = red
	}
	return r
}

// Insert records one action. metadata is redacted first: fields whose name
// looks credential-shaped lose their value, and every string value passes
// through the redactor so a key pasted into a note does not survive.
func (r *AuditRepo) Insert(actorID int64, action, entityType, entityID string, metadata map[string]any) error {
	clean := r.redactMetadata(metadata)
	raw := "{}"
	if len(clean) > 0 {
		encoded, err := json.Marshal(clean)
		if err != nil {
			return err
		}
		raw = string(encoded)
	}
	action = r.red.String(action)
	entityType = r.red.String(entityType)
	entityID = r.red.String(entityID)
	_, err := r.st.DB().Exec(`INSERT INTO audit_events
		(actor_id, action, entity_type, entity_id, metadata, created_at)
		VALUES (?,?,?,?,?,?)`,
		actorID, action, entityType, entityID, raw,
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// List returns recorded events newest first.
func (r *AuditRepo) List(limit int) ([]AuditEvent, error) {
	if limit <= 0 {
		limit = defaultLogLimit
	}
	rows, err := r.st.DB().Query(`SELECT id, actor_id, action, entity_type, entity_id,
		metadata, created_at FROM audit_events ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AuditEvent, 0, limit)
	for rows.Next() {
		var e AuditEvent
		var metadata, created string
		if err := rows.Scan(&e.ID, &e.ActorID, &e.Action, &e.EntityType, &e.EntityID,
			&metadata, &created); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if metadata != "" && metadata != "{}" {
			if err := json.Unmarshal([]byte(metadata), &e.Metadata); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// redactMetadata masks credential-shaped fields and runs the remaining strings
// through the redactor. Nested maps and slices are walked so a secret cannot
// hide one level down.
func (r *AuditRepo) redactMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	return r.redactValue("", metadata).(map[string]any)
}

// redactValue masks by field name and scrubs by value. A field name that
// matches replaces the whole subtree, so the shape of a secret is not left
// behind either.
func (r *AuditRepo) redactValue(name string, value any) any {
	if isSensitiveName(name) {
		return r.mask
	}
	switch v := value.(type) {
	case string:
		return r.red.String(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = r.redactValue(k, item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = r.redactValue(name, item)
		}
		return out
	default:
		// Numbers, booleans and nil carry no secret; structs marshal through
		// their own fields, and encoding/json never sees them here.
		return v
	}
}

// isSensitiveName reports whether a field name suggests it holds a credential.
// Whole words are matched however the name is separated — stream_key, api-key,
// password, access.token — and a camelCase join is caught by looking for a
// lowercase letter immediately before the term, which is what makes streamKey
// and apiToken match. That second rule also masks a harmless name like
// "monkey": over-masking one audit field costs a little context, while
// under-masking a stream key writes it to disk permanently.
func isSensitiveName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	normalized := strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(lower)
	for _, field := range strings.Fields(normalized) {
		for _, part := range sensitiveNameParts {
			if field == part {
				return true
			}
		}
	}
	for _, part := range sensitiveNameParts {
		for at := 0; at < len(lower); {
			found := strings.Index(lower[at:], part)
			if found < 0 {
				break
			}
			start := at + found
			if start > 0 && lower[start-1] >= 'a' && lower[start-1] <= 'z' {
				return true
			}
			at = start + len(part)
		}
	}
	return false
}
