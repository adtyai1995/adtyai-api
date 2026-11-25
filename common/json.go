package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/gin-gonic/gin/binding"
)

type RawMessage = json.RawMessage
type Number = json.Number

// hostJSONCodec is the single place where the host chooses its JSON engine.
// Swap the implementation here (for example to sonic.ConfigStd) and every
// common.* and kitutil.* JSON helper, including relaykit DTO (un)marshalling,
// follows. Injected from init() rather than main() so tests run on the same
// engine as production: common is imported by virtually every root package
// and test binary, while main() never executes under `go test`.
type hostJSONCodec struct{}

func (hostJSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (hostJSONCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (hostJSONCodec) Decode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

func (hostJSONCodec) Valid(data []byte) bool {
	return json.Valid(data)
}

func init() {
	kitutil.SetCodec(hostJSONCodec{})
}

func Unmarshal(data []byte, v any) error {
	return kitutil.Unmarshal(data, v)
}

func UnmarshalJsonStr(data string, v any) error {
	return kitutil.UnmarshalJsonStr(data, v)
}

func DecodeJson(reader io.Reader, v any) error {
	return kitutil.DecodeJson(reader, v)
}

// DecodeJsonObjectUseNumber decodes a single JSON object from a string while
// preserving numbers as json.Number and rejecting trailing tokens.
func DecodeJsonObjectUseNumber(data string) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewBufferString(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, io.ErrUnexpectedEOF
	}
	if raw == nil {
		return map[string]any{}, nil
	}
	return raw, nil
}

// DecodeJsonWithValidation decodes JSON and applies Gin's configured binding-tag
// validator, including binding:"required" and any registered custom validators.
func DecodeJsonWithValidation(reader io.Reader, v any) error {
	if err := DecodeJson(reader, v); err != nil {
		return err
	}
	if binding.Validator == nil {
		return nil
	}
	return binding.Validator.ValidateStruct(v)
}

func Marshal(v any) ([]byte, error) {
	return kitutil.Marshal(v)
}

func IndentJson(data []byte) ([]byte, error) {
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, data, "", "  "); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func GetJsonType(data RawMessage) string {
	return kitutil.GetJsonType(data)
}

const maxValidatedJSONDepth = 512

// CanonicalFoldKey folds each rune to the smallest code point in its
// unicode.SimpleFold equivalence class (matching encoding/json's EqualFold
// field matching) and reports whether any non-ASCII rune folds into ASCII
// (such as U+017F 'ſ' -> 'S'/'s' or U+212A 'K' -> 'K'/'k').
func CanonicalFoldKey(key string) (string, bool) {
	var b strings.Builder
	b.Grow(len(key))
	hasASCIIFoldableNonASCII := false
	for _, r := range key {
		if r < utf8.RuneSelf {
			if r >= 'a' && r <= 'z' {
				b.WriteByte(byte(r - ('a' - 'A')))
			} else {
				b.WriteByte(byte(r))
			}
			continue
		}
		minR := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minR {
				minR = next
			}
		}
		if minR < utf8.RuneSelf {
			hasASCIIFoldableNonASCII = true
		}
		b.WriteRune(minR)
	}
	return b.String(), hasASCIIFoldableNonASCII
}

// ValidateFormFieldKeys rejects form field names that contain non-ASCII runes
// folding into ASCII (SimpleFold spoofing) or distinct keys that collide under
// case/Unicode folding (R10-03).
func ValidateFormFieldKeys(keys []string) error {
	seen := make(map[string]string, len(keys))
	for _, key := range keys {
		folded, spoof := CanonicalFoldKey(key)
		if spoof {
			return fmt.Errorf("invalid form field %q", key)
		}
		if prev, exists := seen[folded]; exists && prev != key {
			return fmt.Errorf("duplicate form field %q is not allowed", key)
		}
		seen[folded] = key
	}
	return nil
}

// ValidateJSONNoDuplicateOrFoldedKeys performs a single O(n) streaming walk
// over the entire JSON tree to reject:
//  1. trailing tokens after the root value;
//  2. any object key at any depth containing non-ASCII runes that SimpleFold
//     into ASCII (e.g. 'ſ', 'K');
//  3. duplicate or case/Unicode-folded colliding keys within the same object
//     at any depth;
//  4. non-canonical case variants of billing/routing-sensitive JSON paths
//     listed in canonicalPaths (dot-separated, e.g. "size", "reasoning.effort").
func ValidateJSONNoDuplicateOrFoldedKeys(data []byte, canonicalPaths []string) error {
	canonicalMap := make(map[string]string, len(canonicalPaths))
	for _, path := range canonicalPaths {
		if path == "" {
			continue
		}
		foldedPath, _ := CanonicalFoldKey(path)
		canonicalMap[foldedPath] = path
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := validateJSONValueKeys(dec, canonicalMap, "", "", 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("invalid JSON request body")
	}
	return nil
}

func validateJSONValueKeys(dec *json.Decoder, canonicalMap map[string]string, exactPath, foldedPath string, depth int) error {
	if depth > maxValidatedJSONDepth {
		return errors.New("JSON nesting depth exceeds limit")
	}
	tok, err := dec.Token()
	if err != nil {
		return errors.New("invalid JSON request body")
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]string)
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return errors.New("invalid JSON request body")
			}
			key, ok := keyTok.(string)
			if !ok {
				return errors.New("invalid JSON request body")
			}
			foldedKey, spoof := CanonicalFoldKey(key)
			if spoof {
				return fmt.Errorf("invalid JSON key %q", key)
			}
			if prev, exists := seen[foldedKey]; exists {
				return fmt.Errorf("duplicate JSON key %q is not allowed", prev)
			}
			seen[foldedKey] = key

			childExact := key
			childFolded := foldedKey
			if exactPath != "" {
				childExact = exactPath + "." + key
				childFolded = foldedPath + "." + foldedKey
			}
			if canonical, sensitive := canonicalMap[childFolded]; sensitive && childExact != canonical {
				return fmt.Errorf("%s must use canonical key casing", canonical)
			}
			if err := validateJSONValueKeys(dec, canonicalMap, childExact, childFolded, depth+1); err != nil {
				return err
			}
		}
		endTok, err := dec.Token()
		if err != nil || endTok != json.Delim('}') {
			return errors.New("invalid JSON request body")
		}
	case '[':
		for dec.More() {
			if err := validateJSONValueKeys(dec, canonicalMap, "", "", depth+1); err != nil {
				return err
			}
		}
		endTok, err := dec.Token()
		if err != nil || endTok != json.Delim(']') {
			return errors.New("invalid JSON request body")
		}
	default:
		return errors.New("invalid JSON request body")
	}
	return nil
}

// JsonRawMessageToString returns JSON strings as their decoded value and other JSON values as raw text.
func JsonRawMessageToString(data RawMessage) string {
	return kitutil.JsonRawMessageToString(data)
}
