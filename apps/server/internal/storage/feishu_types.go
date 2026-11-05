package storage

import "github.com/cofy-x/camp/apps/server/internal/feishuhooks"

type FeishuSource struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	RoomID        string `json:"roomId"`
	AppID         string `json:"appId"`
	TenantKey     string `json:"tenantKey"`
	ChatID        string `json:"chatId"`
	Enabled       bool   `json:"enabled"`
	Revision      int64  `json:"revision"`
	CredentialRef string `json:"-"`
	CreatedBy     string `json:"createdBy"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}
type CreateFeishuSourceInput struct {
	RequestID         string `json:"requestId"`
	AppID             string `json:"appId"`
	TenantKey         string `json:"tenantKey"`
	ChatID            string `json:"chatId"`
	VerificationToken string `json:"verificationToken"`
	EncryptKey        string `json:"encryptKey"`
}
type UpdateFeishuSourceInput struct {
	RequestID         string `json:"requestId"`
	Revision          int64  `json:"revision"`
	Enabled           *bool  `json:"enabled,omitempty"`
	VerificationToken string `json:"verificationToken,omitempty"`
	EncryptKey        string `json:"encryptKey,omitempty"`
}
type FeishuEvent struct {
	feishuhooks.Observation
	Seq            int64  `json:"seq"`
	ID             string `json:"id"`
	SourceID       string `json:"sourceId"`
	RoomID         string `json:"roomId"`
	SourceRevision int64  `json:"sourceRevision"`
	Fingerprint    string `json:"fingerprint"`
	ReceivedAt     string `json:"receivedAt"`
}
type FeishuReceipt struct {
	Kind   string        `json:"kind"`
	Source *FeishuSource `json:"source,omitempty"`
}
