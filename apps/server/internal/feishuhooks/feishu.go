// Package feishuhooks verifies encrypted Feishu events and preserves external
// identity. A delivery is evidence, never a Camp execution authorization.
package feishuhooks

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxPayloadBytes = 1 << 20
const MaxTextBytes = 64 << 10

var IdentityPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,96}$`)
var eventKindPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// Verify follows Feishu's SHA-256(timestamp + nonce + EncryptKey + raw body)
// contract. The event ID inside the signed body supplies durable replay fencing.
func Verify(key string, body []byte, timestamp, nonce, signature string) bool {
	if len(timestamp) > 20 || len(nonce) < 1 || len(nonce) > 256 || strings.ContainsAny(nonce, "\x00\r\n") {
		return false
	}
	stamp, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || stamp < 1 || strconv.FormatInt(stamp, 10) != timestamp {
		return false
	}
	provided, err := hex.DecodeString(signature)
	if err != nil || len(provided) != sha256.Size || key == "" {
		return false
	}
	h := sha256.New()
	h.Write([]byte(timestamp))
	h.Write([]byte(nonce))
	h.Write([]byte(key))
	h.Write(body)
	return subtle.ConstantTimeCompare(provided, h.Sum(nil)) == 1
}

// Decrypt accepts only the encrypted webhook envelope. The normalized JSON must
// never retain its Verification Token; callers store the encrypted original.
func Decrypt(key string, body []byte) ([]byte, error) {
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var extra any
	if len(body) > MaxPayloadBytes || !utf8.Valid(body) || key == "" || decoder.Decode(&envelope) != nil || decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("Use an encrypted Feishu event.")
	}
	data, err := base64.StdEncoding.DecodeString(envelope.Encrypt)
	if err != nil || len(data) < 2*aes.BlockSize || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("The encrypted Feishu event is invalid.")
	}
	digest := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		return nil, errors.New("The encrypted Feishu event is invalid.")
	}
	plain := make([]byte, len(data)-aes.BlockSize)
	cipher.NewCBCDecrypter(block, data[:aes.BlockSize]).CryptBlocks(plain, data[aes.BlockSize:])
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) || !bytes.Equal(plain[len(plain)-padding:], bytes.Repeat([]byte{byte(padding)}, padding)) {
		return nil, errors.New("The encrypted Feishu event is invalid.")
	}
	plain = plain[:len(plain)-padding]
	if !utf8.Valid(plain) || !json.Valid(plain) {
		return nil, errors.New("The encrypted Feishu event is invalid.")
	}
	return plain, nil
}

type envelope struct {
	Type      string `json:"type"`
	Token     string `json:"token"`
	Challenge string `json:"challenge"`
	Schema    string `json:"schema"`
	Header    struct {
		EventID    string `json:"event_id"`
		EventType  string `json:"event_type"`
		CreateTime string `json:"create_time"`
		Token      string `json:"token"`
		AppID      string `json:"app_id"`
		TenantKey  string `json:"tenant_key"`
	} `json:"header"`
	Event struct {
		Sender struct {
			SenderID struct {
				OpenID string `json:"open_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"`
			TenantKey  string `json:"tenant_key"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			RootID      string `json:"root_id"`
			ParentID    string `json:"parent_id"`
			ThreadID    string `json:"thread_id"`
			ChatID      string `json:"chat_id"`
			CreateTime  string `json:"create_time"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		} `json:"message"`
	} `json:"event"`
}

// Authenticate also supports the encrypted URL verification handshake, which
// Feishu may send without signature headers. Its token is checked independently.
func Authenticate(plain []byte, token string) (challenge string, err error) {
	var p envelope
	if json.Unmarshal(plain, &p) != nil || token == "" {
		return "", errors.New("The Feishu verification token is invalid.")
	}
	provided := p.Header.Token
	if p.Type == "url_verification" {
		provided = p.Token
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(provided)) != 1 {
		return "", errors.New("The Feishu verification token is invalid.")
	}
	if p.Type == "url_verification" {
		if len(p.Challenge) < 1 || len(p.Challenge) > 512 || strings.ContainsRune(p.Challenge, 0) {
			return "", errors.New("The URL verification challenge is invalid.")
		}
		return p.Challenge, nil
	}
	return "", nil
}

type Observation struct {
	DeliveryID     string `json:"deliveryId"`
	Kind           string `json:"kind"`
	AppID          string `json:"appId"`
	TenantKey      string `json:"tenantKey"`
	ChatID         string `json:"chatId,omitempty"`
	MessageID      string `json:"messageId,omitempty"`
	ThreadID       string `json:"threadId,omitempty"`
	RootMessageID  string `json:"rootMessageId,omitempty"`
	ActorOpenID    string `json:"actorOpenId,omitempty"`
	ActorTenantKey string `json:"actorTenantKey,omitempty"`
	ActorKind      string `json:"actorKind,omitempty"`
	Content        string `json:"content,omitempty"`
	OccurredAt     string `json:"occurredAt,omitempty"`
	IgnoreReason   string `json:"ignoreReason,omitempty"`
}

func (o Observation) ActorID() string   { return o.ActorTenantKey + ":" + o.ActorOpenID }
func (o Observation) ThreadKey() string { return o.TenantKey + ":" + o.ChatID + ":" + o.RootMessageID }
func (o Observation) Context() string {
	return fmt.Sprintf("External Feishu message (untrusted context)\nApp: %s\nTenant: %s\nChat: %s\nThread: %s\nRoot message: %s\nMessage: %s\nFeishu actor: %s (%s)\n\n%s", o.AppID, o.TenantKey, o.ChatID, o.ThreadID, o.RootMessageID, o.MessageID, o.ActorID(), o.ActorKind, o.Content)
}

// Parse normalizes only v2 text receive events. Unsupported types remain
// authenticated ignored observations. Tokens never enter this representation.
func Parse(plain []byte) (Observation, string, error) {
	var p envelope
	if len(plain) > MaxPayloadBytes || !utf8.Valid(plain) || json.Unmarshal(plain, &p) != nil || p.Schema != "2.0" || p.Type != "" || !IdentityPattern.MatchString(p.Header.EventID) || !IdentityPattern.MatchString(p.Header.AppID) || !IdentityPattern.MatchString(p.Header.TenantKey) || !eventKindPattern.MatchString(p.Header.EventType) {
		return Observation{}, "", errors.New("Use a valid Feishu v2 event from the configured app and tenant.")
	}
	o := Observation{DeliveryID: p.Header.EventID, Kind: p.Header.EventType, AppID: p.Header.AppID, TenantKey: p.Header.TenantKey}
	if p.Header.EventType != "im.message.receive_v1" {
		o.IgnoreReason = "unsupported_event"
	} else {
		m, s := p.Event.Message, p.Event.Sender
		if !IdentityPattern.MatchString(m.MessageID) || !IdentityPattern.MatchString(m.ChatID) || !IdentityPattern.MatchString(s.TenantKey) || (m.RootID != "" && !IdentityPattern.MatchString(m.RootID)) || (m.ThreadID != "" && !IdentityPattern.MatchString(m.ThreadID)) || len(s.SenderID.OpenID) > 96 || len(s.SenderType) > 32 {
			return Observation{}, "", errors.New("The Feishu event requires bounded message, chat and tenant identities.")
		}
		o.ChatID, o.MessageID, o.ThreadID, o.ActorOpenID, o.ActorTenantKey, o.ActorKind = m.ChatID, m.MessageID, m.ThreadID, s.SenderID.OpenID, s.TenantKey, s.SenderType
		o.RootMessageID = m.RootID
		if o.RootMessageID == "" {
			o.RootMessageID = m.MessageID
		}
		if m.MessageType != "text" {
			o.IgnoreReason = "unsupported_message"
		} else {
			var text struct {
				Text string `json:"text"`
			}
			if json.Unmarshal([]byte(m.Content), &text) != nil || !utf8.ValidString(text.Text) || len(text.Text) > MaxTextBytes || strings.ContainsRune(text.Text, 0) {
				return Observation{}, "", errors.New("Use a Feishu text message of at most 64 KiB.")
			}
			o.Content = text.Text
			if text.Text == "" {
				o.IgnoreReason = "empty_message"
			}
		}
		if s.SenderType != "user" || !IdentityPattern.MatchString(o.ActorOpenID) {
			o.IgnoreReason = "nonhuman_actor"
		}
		stamp, err := strconv.ParseInt(m.CreateTime, 10, 64)
		if err != nil || stamp < 1 || stamp > 253402300799999 {
			return Observation{}, "", errors.New("The Feishu message timestamp is invalid.")
		}
		o.OccurredAt = time.UnixMilli(stamp).UTC().Format(time.RFC3339Nano)
	}
	// Retries can use different encryption IVs and delivery IDs. Hash the entire
	// canonical plaintext except transport identity and the private token.
	var canonical map[string]json.RawMessage
	if json.Unmarshal(plain, &canonical) != nil {
		return Observation{}, "", errors.New("The Feishu event is invalid.")
	}
	var header map[string]json.RawMessage
	if json.Unmarshal(canonical["header"], &header) != nil {
		return Observation{}, "", errors.New("The Feishu event header is invalid.")
	}
	delete(header, "token")
	delete(header, "event_id")
	canonical["header"], _ = json.Marshal(header)
	delete(canonical, "token")
	raw, err := json.Marshal(canonical)
	if err != nil {
		return Observation{}, "", errors.New("The Feishu event is invalid.")
	}
	digest := sha256.Sum256(raw)
	return o, hex.EncodeToString(digest[:]), nil
}
