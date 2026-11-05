package orchestration

// Memory is quoted, room-scoped evidence. It never supplies execution authority.
// Each revision retains its original bytes; Current is a read projection.
type Memory struct {
	ID                 string           `json:"id"`
	RoomID             string           `json:"roomId"`
	Revision           int64            `json:"revision"`
	SupersedesRevision int64            `json:"supersedesRevision,omitempty"`
	State              string           `json:"state"`
	Content            string           `json:"content"`
	Provenance         MemoryProvenance `json:"provenance"`
	AuthorUserID       string           `json:"authorUserId"`
	RecordedByUserID   string           `json:"recordedByUserId"`
	CreatedAt          string           `json:"createdAt"`
	Current            bool             `json:"current"`
}

// A human note explicitly names its author instead of inventing source evidence.
// Other provenance points to immutable originals owned by the same Room.
type MemoryProvenance struct {
	Kind          string `json:"kind"`
	MessageID     string `json:"messageId,omitempty"`
	SourceEventID string `json:"sourceEventId,omitempty"`
	ResourceID    string `json:"resourceId,omitempty"`
	Commit        string `json:"commit,omitempty"`
	Path          string `json:"path,omitempty"`
}

type MemoryInput struct {
	RequestID  string           `json:"requestId"`
	Revision   int64            `json:"revision"`
	Content    string           `json:"content"`
	Provenance MemoryProvenance `json:"provenance"`
}

type MemoryRetraction struct {
	RequestID string `json:"requestId"`
	Revision  int64  `json:"revision"`
}

type MemoryReceipt struct {
	Kind   string  `json:"kind"`
	Memory *Memory `json:"memory,omitempty"`
}
