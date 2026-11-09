// Package syncsvc implements push and pull of per-user LWW records.
package syncsvc

import (
	"encoding/json"

	"github.com/aluo96078/shinsou_sync_service/internal/lww"
)

// Limits from the API contract.
const (
	MaxRecordsPerBatch = 500
	MaxWorksPerBatch   = 500
	MaxBodyBytes       = 1 << 20
	MaxChangesLimit    = 500
)

// Work is a work descriptor (docs/spec/records.md section 4).
type Work struct {
	KeyVersion    int     `json:"keyVersion"`
	PackageID     string  `json:"packageId"`
	SourceID      string  `json:"sourceId"`
	RemoteID      string  `json:"remoteId"`
	Hash          string  `json:"hash"`
	Title         string  `json:"title"`
	Author        *string `json:"author,omitempty"`
	CoverURL      *string `json:"coverUrl,omitempty"`
	ContentKind   string  `json:"contentKind"`
	Language      *string `json:"language,omitempty"`
	SourceName    *string `json:"sourceName,omitempty"`
	RepositoryURL *string `json:"repositoryUrl,omitempty"`
}

// PushDeleted is the deleted register as pushed; value is a pointer so a missing value is
// detectable.
type PushDeleted struct {
	Value *bool  `json:"value"`
	Clock string `json:"clock"`
}

// PushRecord is one pushed record.
type PushRecord struct {
	Kind         string                     `json:"kind"`
	Key          string                     `json:"key"`
	Work         string                     `json:"work,omitempty"`
	UnitRemoteID *string                    `json:"unitRemoteId,omitempty"`
	Fields       map[string]json.RawMessage `json:"fields,omitempty"`
	Clocks       map[string]string          `json:"clocks,omitempty"`
	Deleted      *PushDeleted               `json:"deleted,omitempty"`
}

// PushRequest is the body of POST /sync/push.
type PushRequest struct {
	BatchID string       `json:"batchId"`
	Since   *int64       `json:"since"`
	Works   []Work       `json:"works,omitempty"`
	Records []PushRecord `json:"records"`
}

// SyncRecord is a full record state as returned to clients.
type SyncRecord struct {
	Kind         string                     `json:"kind"`
	Key          string                     `json:"key"`
	Seq          int64                      `json:"seq"`
	Work         *Work                      `json:"work,omitempty"`
	UnitRemoteID string                     `json:"unitRemoteId,omitempty"`
	Fields       map[string]json.RawMessage `json:"fields"`
	Clocks       map[string]string          `json:"clocks"`
	Deleted      *lww.Register              `json:"deleted,omitempty"`
	Cleared      string                     `json:"cleared,omitempty"`
}

// PushResult is the outcome for one pushed record.
type PushResult struct {
	Kind   string      `json:"kind"`
	Key    string      `json:"key"`
	Result string      `json:"result"`
	Record *SyncRecord `json:"record,omitempty"`
}

// Extra per-record outcomes beyond the lww results.
const (
	ResultInvalidKey    = "rejected:invalid_key"
	ResultUnknownWork   = "rejected:unknown_work"
	ResultQuotaExceeded = "rejected:quota_exceeded"
)

// PushResponse is the body returned by POST /sync/push.
type PushResponse struct {
	Results []PushResult `json:"results"`
	Seq     int64        `json:"seq"`
}

// ChangesResponse is the body returned by GET /sync/changes.
type ChangesResponse struct {
	Records   []SyncRecord `json:"records"`
	NextSince int64        `json:"nextSince"`
	HasMore   bool         `json:"hasMore"`
}
