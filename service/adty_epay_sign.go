package service

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

var (
	// ErrInvalidEpayRSAKey means an Epay RSA private or public key is missing or malformed.
	ErrInvalidEpayRSAKey = errors.New("epay: invalid RSA key")
	// ErrInvalidEpayParams means the parameters cannot safely form a canonical Epay signing string.
	ErrInvalidEpayParams = errors.New("epay: invalid signing parameters")
	// ErrInvalidEpaySignature means an Epay RSA signature did not match the platform public key.
	ErrInvalidEpaySignature = errors.New("epay: invalid signature")
	// ErrInvalidEpayTimestamp means an Epay RSA payload is missing a timestamp or outside EpayTimestampTolerance.
	ErrInvalidEpayTimestamp = errors.New("epay: invalid timestamp")
)

// EpayTimestampTolerance is the maximum allowed clock skew for Epay v2 RSA callbacks and responses.
const EpayTimestampTolerance = 5 * time.Minute

// adtyEpayNow returns the current time and may be overridden in tests.
var adtyEpayNow = time.Now

type epayRSAAPIError struct {
	Code string
	Msg  string
}

func (e *epayRSAAPIError) Error() string {
	return fmt.Sprintf("epay api code %s: %s", e.Code, e.Msg)
}

// EpaySign 和 go-epay v0.0.5 一致：去掉 sign、sign_type 和空值，按键排序拼 k=v&…，接密钥取 md5。
// 参数有歧义（键为空或含 &、=，值含 &）时报错，不出签名（任务 5.1）。
func EpaySign(params map[string]string, key string) (string, error) {
	if params == nil {
		return "", fmt.Errorf("%w: nil parameter map", epay.ErrInvalidParams)
	}
	signed, err := epay.GenerateParamsWithError(maps.Clone(params), key)
	if err != nil {
		return "", err
	}
	return signed["sign"], nil
}

// EpayVerify 用这个通道的密钥验签，常量时间比较；参数有歧义时判失败。
func EpayVerify(params map[string]string, key string) bool {
	got := strings.ToLower(strings.TrimSpace(params["sign"]))
	if got == "" || key == "" {
		return false
	}
	want, err := EpaySign(params, key)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// ParseEpayRSAPrivateKey parses a merchant RSA private key.
// Accepts PEM text or raw/wrapped Base64 DER in PKCS#8 or PKCS#1 format.
func ParseEpayRSAPrivateKey(key string) (*rsa.PrivateKey, error) {
	der, err := decodeEpayRSAKeyDER(key)
	if err != nil {
		return nil, err
	}
	if parsed, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		privateKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%w: not an RSA private key", ErrInvalidEpayRSAKey)
		}
		return privateKey, nil
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("%w: unsupported private key format", ErrInvalidEpayRSAKey)
	}
	return privateKey, nil
}

// ParseEpayRSAPublicKey parses a platform RSA public key.
// Accepts PEM text or raw/wrapped Base64 DER in PKIX or PKCS#1 format.
func ParseEpayRSAPublicKey(key string) (*rsa.PublicKey, error) {
	der, err := decodeEpayRSAKeyDER(key)
	if err != nil {
		return nil, err
	}
	if parsed, err := x509.ParsePKIXPublicKey(der); err == nil {
		publicKey, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: not an RSA public key", ErrInvalidEpayRSAKey)
		}
		return publicKey, nil
	}
	publicKey, err := x509.ParsePKCS1PublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("%w: unsupported public key format", ErrInvalidEpayRSAKey)
	}
	return publicKey, nil
}

func decodeEpayRSAKeyDER(key string) ([]byte, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("%w: empty key", ErrInvalidEpayRSAKey)
	}
	if strings.HasPrefix(key, "-----BEGIN") {
		block, _ := pem.Decode([]byte(key))
		if block == nil {
			return nil, fmt.Errorf("%w: malformed PEM block", ErrInvalidEpayRSAKey)
		}
		return block.Bytes, nil
	}
	der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(key), ""))
	if err != nil {
		return nil, fmt.Errorf("%w: key is neither PEM nor Base64", ErrInvalidEpayRSAKey)
	}
	return der, nil
}

// epayRSASignContent builds the canonical Epay v2 signing string:
// excludes sign, sign_type, and empty/whitespace-only values; sorts keys by ASCII;
// joins key=value with '&' without URL-encoding values.
func epayRSASignContent(params map[string]string, allowAmpersandValues bool) (string, error) {
	if params == nil {
		return "", fmt.Errorf("%w: nil parameter map", ErrInvalidEpayParams)
	}
	keys := make([]string, 0, len(params))
	for key, value := range params {
		if key == "sign" || key == "sign_type" || strings.Trim(value, " \t\n\r\x00\x0b") == "" {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var content strings.Builder
	for i, key := range keys {
		if key == "" || strings.ContainsAny(key, "&=") {
			return "", fmt.Errorf("%w: invalid parameter name %q", ErrInvalidEpayParams, key)
		}
		value := params[key]
		if !allowAmpersandValues && strings.Contains(value, "&") {
			return "", fmt.Errorf("%w: parameter %q contains '&' in its value", ErrInvalidEpayParams, key)
		}
		if i > 0 {
			content.WriteByte('&')
		}
		content.WriteString(key)
		content.WriteByte('=')
		content.WriteString(value)
	}
	return content.String(), nil
}

// EpaySignRSA signs params using SHA256WithRSA (PKCS#1 v1.5) and returns Base64.
func EpaySignRSA(params map[string]string, privateKey string) (string, error) {
	parsedKey, err := ParseEpayRSAPrivateKey(privateKey)
	if err != nil {
		return "", err
	}
	content, err := epayRSASignContent(params, false)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(content))
	signature, err := rsa.SignPKCS1v15(rand.Reader, parsedKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

// EpayVerifyRSA verifies an Epay v2 RSA callback payload (signature + timestamp window).
func EpayVerifyRSA(params map[string]string, publicKey string) (bool, error) {
	return verifyEpayRSA(params, publicKey, false)
}

func verifyEpayRSA(params map[string]string, publicKey string, allowAmpersandValues bool) (bool, error) {
	parsedKey, err := ParseEpayRSAPublicKey(publicKey)
	if err != nil {
		return false, err
	}
	content, err := epayRSASignContent(params, allowAmpersandValues)
	if err != nil {
		return false, err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(params["sign"]))
	if err != nil || len(signature) == 0 {
		return false, nil
	}
	digest := sha256.Sum256([]byte(content))
	if rsa.VerifyPKCS1v15(parsedKey, crypto.SHA256, digest[:], signature) != nil {
		return false, nil
	}
	timestamp, err := strconv.ParseInt(strings.TrimSpace(params["timestamp"]), 10, 64)
	if err != nil {
		return false, fmt.Errorf("%w: %q", ErrInvalidEpayTimestamp, params["timestamp"])
	}
	skew := adtyEpayNow().Unix() - timestamp
	tolerance := int64(EpayTimestampTolerance / time.Second)
	if skew > tolerance || skew < -tolerance {
		return false, fmt.Errorf("%w: %q is outside the allowed window", ErrInvalidEpayTimestamp, params["timestamp"])
	}
	return true, nil
}

// EpaySignChannelParams returns a signed copy of params for the given channel without mutating input.
// It always populates pid, sign, and sign_type, and adds a 10-digit unix timestamp for RSA channels.
func EpaySignChannelParams(channel *model.AdtyEpayChannel, params map[string]string) (map[string]string, error) {
	if channel == nil {
		return nil, fmt.Errorf("%w: nil channel", ErrInvalidEpayParams)
	}
	if params == nil {
		return nil, fmt.Errorf("%w: nil parameter map", ErrInvalidEpayParams)
	}
	signed := make(map[string]string, len(params)+4)
	maps.Copy(signed, params)
	signed["pid"] = channel.MerchantId
	if !channel.IsRSA() {
		sign, err := EpaySign(signed, channel.MerchantKey)
		if err != nil {
			return nil, err
		}
		signed["sign"] = sign
		signed["sign_type"] = model.AdtyEpaySignTypeMD5
		return signed, nil
	}
	signed["timestamp"] = strconv.FormatInt(adtyEpayNow().Unix(), 10)
	sign, err := EpaySignRSA(signed, channel.MerchantPrivateKey)
	if err != nil {
		return nil, err
	}
	signed["sign"] = sign
	signed["sign_type"] = model.AdtyEpaySignTypeRSA
	return signed, nil
}

// EpayVerifyCallback validates an incoming Epay webhook/notify callback for a channel.
// It verifies partner ID (pid) BEFORE cryptographic signature verification (#8422)
// and enforces that the callback sign_type matches the channel's configured SignType.
func EpayVerifyCallback(channel *model.AdtyEpayChannel, params map[string]string) bool {
	if channel == nil || params == nil || channel.Status == model.AdtyEpayStatusRevoked || !channel.HasCredentials() {
		return false
	}
	expectedPID := strings.TrimSpace(channel.MerchantId)
	if expectedPID == "" || params["pid"] != expectedPID {
		return false
	}
	callbackSignType := strings.TrimSpace(params["sign_type"])
	if channel.IsRSA() {
		if !strings.EqualFold(callbackSignType, model.AdtyEpaySignTypeRSA) {
			return false
		}
		verified, err := verifyEpayRSA(params, channel.PlatformPublicKey, false)
		return err == nil && verified
	}
	if callbackSignType != "" && !strings.EqualFold(callbackSignType, model.AdtyEpaySignTypeMD5) {
		return false
	}
	return EpayVerify(params, channel.MerchantKey)
}

// EpayVerifyRSAResponse decodes and verifies a JSON response from an Epay v2 RSA API endpoint.
// Numeric literals are preserved verbatim; true becomes "1"; false and null become "";
// objects and arrays do not participate in signing and are omitted.
func EpayVerifyRSAResponse(channel *model.AdtyEpayChannel, body []byte) (map[string]string, error) {
	if channel == nil || !channel.IsRSA() {
		return nil, errors.New("epay: response verification requires an RSA channel")
	}
	var raw map[string]common.RawMessage
	if err := common.Unmarshal(body, &raw); err != nil || raw == nil {
		return nil, fmt.Errorf("epay: decode response failed")
	}
	fields := make(map[string]string, len(raw))
	for key, value := range raw {
		trimmed := strings.TrimSpace(string(value))
		if trimmed == "" || trimmed == "null" {
			fields[key] = ""
			continue
		}
		switch trimmed[0] {
		case '"':
			var str string
			if err := common.Unmarshal(value, &str); err != nil {
				return nil, fmt.Errorf("epay: decode response field %q failed", key)
			}
			fields[key] = str
		case '{', '[':
			continue
		default:
			if trimmed == "true" {
				fields[key] = "1"
			} else if trimmed == "false" {
				fields[key] = ""
			} else {
				fields[key] = trimmed
			}
		}
	}
	if fields["code"] != "0" {
		return nil, &epayRSAAPIError{Code: fields["code"], Msg: fields["msg"]}
	}
	if st := strings.TrimSpace(fields["sign_type"]); st != "" && !strings.EqualFold(st, model.AdtyEpaySignTypeRSA) {
		return nil, ErrInvalidEpaySignature
	}
	verified, err := verifyEpayRSA(fields, channel.PlatformPublicKey, true)
	if err != nil {
		return nil, err
	}
	if !verified {
		return nil, ErrInvalidEpaySignature
	}
	return fields, nil
}
