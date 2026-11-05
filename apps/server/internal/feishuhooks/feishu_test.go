package feishuhooks

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

const key = "synthetic-encryption-key"
const token = "synthetic-verification-token"

func encrypted(t *testing.T, plain []byte) []byte {
	t.Helper()
	digest := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	data := append(bytes.Clone(plain), bytes.Repeat([]byte{byte(padding)}, padding)...)
	iv := bytes.Repeat([]byte{0x7a}, aes.BlockSize)
	buf := append(bytes.Clone(iv), make([]byte, len(data))...)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(buf[aes.BlockSize:], data)
	raw, _ := json.Marshal(map[string]string{"encrypt": base64.StdEncoding.EncodeToString(buf)})
	return raw
}
func message(body string) []byte {
	text, _ := json.Marshal(map[string]string{"text": body})
	raw, _ := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]string{"event_id": "delivery-1", "event_type": "im.message.receive_v1", "app_id": "cli_app", "tenant_key": "tenant1", "token": token}, "event": map[string]any{"sender": map[string]any{"sender_type": "user", "tenant_key": "tenant2", "sender_id": map[string]string{"open_id": "ou_actor"}}, "message": map[string]string{"message_id": "om_message", "chat_id": "oc_chat", "root_id": "om_root", "create_time": "1700000000000", "message_type": "text", "content": string(text)}}})
	return raw
}

func TestFeishuEncryptionSignatureTokenAndHandshake(t *testing.T) {
	plain := message("  Original\ntext \n")
	raw := encrypted(t, plain)
	decoded, err := Decrypt(key, raw)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatal("decryption changed original", err)
	}
	digest := sha256.Sum256(append([]byte("1700000000"+"nonce"+key), raw...))
	signature := hex.EncodeToString(digest[:])
	if !Verify(key, raw, "1700000000", "nonce", signature) {
		t.Fatal("valid signature rejected")
	}
	for _, input := range []struct {
		body                        []byte
		timestamp, nonce, signature string
	}{{append(bytes.Clone(raw), ' '), "1700000000", "nonce", signature}, {raw, "1700000001", "nonce", signature}, {raw, "1700000000", "other", signature}, {raw, "1700000000", "nonce", strings.Repeat("0", 64)}, {raw, "-1", "nonce", signature}, {raw, "1700000000", "", signature}} {
		if Verify(key, input.body, input.timestamp, input.nonce, input.signature) {
			t.Fatal("accepted altered signature")
		}
	}
	if challenge, err := Authenticate(decoded, token); err != nil || challenge != "" {
		t.Fatal(challenge, err)
	}
	if _, err := Authenticate(decoded, token+"wrong"); err == nil {
		t.Fatal("accepted wrong token")
	}
	challenge, _ := json.Marshal(map[string]string{"type": "url_verification", "token": token, "challenge": "quoted\"challenge"})
	if got, err := Authenticate(challenge, token); err != nil || got != "quoted\"challenge" {
		t.Fatal(got, err)
	}
	for _, bad := range [][]byte{[]byte(`{}`), []byte(`{"encrypt":"bad"}`), []byte(`{"encrypt":"AAAAAAAAAAAAAAAAAAAAAA=="}`), plain} {
		if _, err := Decrypt(key, bad); err == nil {
			t.Fatal("accepted invalid encrypted body")
		}
	}
	if _, err := Decrypt(key+"wrong", raw); err == nil {
		t.Fatal("accepted wrong encryption key")
	}
}

func TestFeishuNormalizationPreservesScopedIdentityAndDeduplicatesReencryption(t *testing.T) {
	plain := message("  Original\ntext \n")
	o, fingerprint, err := Parse(plain)
	if err != nil || o.Content != "  Original\ntext \n" || o.ActorID() != "tenant2:ou_actor" || o.ThreadKey() != "tenant1:oc_chat:om_root" || o.IgnoreReason != "" {
		t.Fatal(o, err)
	}
	serialized, _ := json.Marshal(o)
	if bytes.Contains(serialized, []byte(token)) {
		t.Fatal("retained verification token")
	}
	changed := bytes.ReplaceAll(plain, []byte("delivery-1"), []byte("delivery-2"))
	changed = bytes.ReplaceAll(changed, []byte(token), []byte(token+"rotated"))
	_, next, err := Parse(changed)
	if err != nil || next != fingerprint {
		t.Fatal("transport redelivery changed fact identity", err)
	}
	_, next, err = Parse(message("different content"))
	if err != nil || next == fingerprint {
		t.Fatal("content change lost fingerprint", err)
	}
	bot := bytes.Replace(plain, []byte(`"sender_type":"user"`), []byte(`"sender_type":"bot"`), 1)
	if got, _, err := Parse(bot); err != nil || got.IgnoreReason != "nonhuman_actor" {
		t.Fatal(got, err)
	}
	unsupported := bytes.Replace(plain, []byte(`"message_type":"text"`), []byte(`"message_type":"image"`), 1)
	if got, _, err := Parse(unsupported); err != nil || got.IgnoreReason != "unsupported_message" {
		t.Fatal(got, err)
	}
	for _, bad := range [][]byte{message(strings.Repeat("x", MaxTextBytes+1)), message("bad\x00text"), bytes.Replace(plain, []byte(`"schema":"2.0"`), []byte(`"schema":"1.0"`), 1)} {
		if _, _, err := Parse(bad); err == nil {
			t.Fatal("accepted malformed or oversized context")
		}
	}
}
