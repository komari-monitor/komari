// Package crypto provides a Node.js-compatible crypto module backed by the Go
// standard library and golang.org/x/crypto. It is registered by jsruntime as
// both "crypto" and "node:crypto" when NodeJS mode is enabled.
//
// The implementation covers the commonly used subset: hash/HMAC, random
// generation, PBKDF2/scrypt, symmetric ciphers (AES-CBC/CTR/ECB/GCM and
// ChaCha20-Poly1305), timing-safe comparison and RSA/ECDSA/Ed25519
// sign/verify with PEM keys. See the jsruntime README for compatibility
// notes and known deviations.
package crypto

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"math"
	"math/big"
	"reflect"
	"strings"

	moejs "github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/buffer"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/ripemd160"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/crypto/sha3"
	"golang.org/x/text/encoding/unicode"
)

// ModuleName is the CommonJS name of the Node.js crypto compatibility module.
const ModuleName = "crypto"

// Module implements the crypto module host services. It keeps the bridge
// runtime so asynchronous variants (randomBytes, pbkdf2, scrypt, sign, ...)
// can schedule callbacks on the JavaScript event loop.
type Module struct {
	runtime *bridge.Runtime
}

// New creates a crypto module bound to the given runtime services.
func New(runtime *bridge.Runtime) *Module {
	return &Module{runtime: runtime}
}

// Load registers the Node.js-compatible crypto module exports.
func (m *Module) Load(vm *moejs.Runtime, module *engine.Object) {
	exports := vm.Realm().NewObject()

	hashCtor := constructor(vm, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		state := newDigestState(vm, stringArg(vm, moejs.Arg(args, 0)), nil, false)
		m.attachDigestMethods(vm, this.AsObject(), state)
		return this, nil
	})
	hmacCtor := constructor(vm, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		state := newDigestState(vm, stringArg(vm, moejs.Arg(args, 0)), m.decodeData(vm, moejs.Arg(args, 1), ""), true)
		m.attachDigestMethods(vm, this.AsObject(), state)
		return this, nil
	})
	signCtor := m.makeSignConstructor(vm, true)
	verifyCtor := m.makeSignConstructor(vm, false)
	cipherCtor := m.makeCipherConstructor(vm, true)
	decipherCtor := m.makeCipherConstructor(vm, false)

	_ = native.Set(vm, exports, "Hash", hashCtor)
	_ = native.Set(vm, exports, "Hmac", hmacCtor)
	_ = native.Set(vm, exports, "Sign", signCtor)
	_ = native.Set(vm, exports, "Verify", verifyCtor)
	_ = native.Set(vm, exports, "Cipher", cipherCtor)
	_ = native.Set(vm, exports, "Decipher", decipherCtor)

	_ = native.Function(vm, exports, "createHash", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.createHash(vm, args, hashCtor), nil
	})
	_ = native.Function(vm, exports, "createHmac", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.createHmac(vm, args, hmacCtor), nil
	})
	_ = native.Function(vm, exports, "createCipheriv", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.createCipheriv(vm, args, cipherCtor), nil
	})
	_ = native.Function(vm, exports, "createDecipheriv", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.createCipheriv(vm, args, decipherCtor), nil
	})
	_ = native.Function(vm, exports, "createSign", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.createSign(vm, args, signCtor), nil
	})
	_ = native.Function(vm, exports, "createVerify", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.createSign(vm, args, verifyCtor), nil
	})
	_ = native.Function(vm, exports, "sign", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.signOneShot(vm, args, true), nil
	})
	_ = native.Function(vm, exports, "verify", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.signOneShot(vm, args, false), nil
	})

	_ = native.Function(vm, exports, "randomBytes", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.randomBytes(vm, args), nil
	})
	_ = native.Function(vm, exports, "randomFillSync", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.randomFill(vm, args, true), nil
	})
	_ = native.Function(vm, exports, "randomFill", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.randomFill(vm, args, false), nil
	})
	_ = native.Function(vm, exports, "randomInt", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.randomInt(vm, args), nil
	})
	_ = native.Function(vm, exports, "randomUUID", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.randomUUID(vm, args), nil
	})
	_ = native.Function(vm, exports, "getRandomValues", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.getRandomValues(vm, args), nil
	})

	_ = native.Function(vm, exports, "timingSafeEqual", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.timingSafeEqual(vm, args), nil
	})

	_ = native.Function(vm, exports, "pbkdf2", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.pbkdf2(vm, args, false), nil
	})
	_ = native.Function(vm, exports, "pbkdf2Sync", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.pbkdf2(vm, args, true), nil
	})
	_ = native.Function(vm, exports, "scrypt", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.scrypt(vm, args, false), nil
	})
	_ = native.Function(vm, exports, "scryptSync", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.scrypt(vm, args, true), nil
	})

	_ = native.Function(vm, exports, "hash", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return m.hashOneShot(vm, args), nil
	})
	_ = native.Function(vm, exports, "getHashes", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, hashNames()), nil
	})
	_ = native.Function(vm, exports, "getCiphers", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, cipherNames()), nil
	})
	_ = native.Set(vm, exports, "constants", map[string]int{
		"RSA_PKCS1_PADDING":      1,
		"RSA_SSLV23_PADDING":     2,
		"RSA_NO_PADDING":         3,
		"RSA_PKCS1_OAEP_PADDING": 4,
		"RSA_X931_PADDING":       5,
		"RSA_PKCS1_PSS_PADDING":  6,
	})

	_ = native.Set(vm, module, "exports", exports)
}

// ---------------------------------------------------------------------------
// Small helpers

func argAt(arguments []moejs.Value, index int) moejs.Value {
	if index < 0 || index >= len(arguments) {
		return moejs.Undefined()
	}
	return arguments[index]
}

func stringArg(vm *moejs.Runtime, value moejs.Value) string {
	if value.IsUndefined() || value.IsNull() {
		return ""
	}
	return native.String(vm, value)
}

// cryptoError is an error with a Node-style error code.
type cryptoError struct {
	code    string
	message string
}

func (e *cryptoError) Error() string { return e.message }

func newCryptoError(code, message string) *cryptoError {
	return &cryptoError{code: code, message: message}
}

// cryptoThrow panics with a JavaScript Error that carries a Node-style code.
func cryptoThrow(vm *moejs.Runtime, code, message string) {
	panic(cryptoErrorObject(vm, newCryptoError(code, message)))
}

// cryptoErrorObject converts an error into a JavaScript Error, attaching the
// Node-style code property when the error carries one.
func cryptoErrorObject(vm *moejs.Runtime, err error) moejs.Value {
	object := native.GoError(vm, err)
	var coded *cryptoError
	if errors.As(err, &coded) && coded.code != "" {
		_ = native.Set(vm, object.AsObject(), "code", coded.code)
	}
	return object
}

func invalidArgType(vm *moejs.Runtime, name, expected string) {
	panic(native.TypeError(vm, "%s", fmt.Sprintf("The %q argument must be %s", name, expected)))
}

func outOfRange(vm *moejs.Runtime, name, message string) {
	cryptoThrow(vm, "ERR_OUT_OF_RANGE", fmt.Sprintf("The value of %q is out of range. %s", name, message))
}

// ---------------------------------------------------------------------------
// Encodings

// decodeData converts a JavaScript value to bytes. Strings are decoded with
// the optional input encoding (utf8 by default); Buffer, TypedArray, DataView
// and ArrayBuffer values are taken as raw bytes.
func (m *Module) decodeData(vm *moejs.Runtime, value moejs.Value, encoding string) []byte {
	if value.IsUndefined() || value.IsNull() {
		invalidArgType(vm, "data", "of type string or an instance of Buffer, TypedArray, or DataView")
	}
	if text, ok := native.Export(vm, value).(string); ok {
		return decodeString(vm, text, encoding)
	}
	if !isArrayBufferView(vm, value) {
		invalidArgType(vm, "data", "of type string or an instance of Buffer, TypedArray, or DataView")
	}
	return append([]byte(nil), viewBytes(vm, value)...)
}

func decodeString(vm *moejs.Runtime, text, encoding string) []byte {
	switch strings.ToLower(encoding) {
	case "", "utf8", "utf-8":
		return []byte(text)
	case "hex":
		data, err := hex.DecodeString(text)
		if err != nil {
			panic(native.TypeError(vm, "Invalid hex string"))
		}
		return data
	case "base64":
		data, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			if data, err = base64.RawStdEncoding.DecodeString(text); err != nil {
				panic(native.TypeError(vm, "Invalid base64 string"))
			}
		}
		return data
	case "base64url":
		data, err := base64.RawURLEncoding.DecodeString(text)
		if err != nil {
			if data, err = base64.URLEncoding.DecodeString(text); err != nil {
				panic(native.TypeError(vm, "Invalid base64url string"))
			}
		}
		return data
	case "latin1", "binary":
		out := make([]byte, 0, len(text))
		for _, r := range text {
			out = append(out, byte(r))
		}
		return out
	case "ascii":
		out := make([]byte, 0, len(text))
		for _, r := range text {
			out = append(out, byte(r&0x7f))
		}
		return out
	case "utf16le", "ucs2", "ucs-2", "utf-16le":
		out, err := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder().Bytes([]byte(text))
		if err != nil {
			panic(native.TypeError(vm, "Invalid utf16le string"))
		}
		return out
	default:
		panic(native.TypeError(vm, "%s", fmt.Sprintf("Unknown encoding: %s", encoding)))
	}
}

// encodeOutput converts bytes to a Buffer (default) or an encoded string.
func encodeOutput(vm *moejs.Runtime, data []byte, encoding string) moejs.Value {
	if encoding == "" {
		return engine.ObjectValue(buffer.WrapBytes(vm, data))
	}
	switch strings.ToLower(encoding) {
	case "hex":
		return native.From(vm, hex.EncodeToString(data))
	case "base64":
		return native.From(vm, base64.StdEncoding.EncodeToString(data))
	case "base64url":
		return native.From(vm, base64.RawURLEncoding.EncodeToString(data))
	case "utf8", "utf-8":
		return native.From(vm, string(data))
	case "latin1", "binary":
		return native.From(vm, latin1String(data))
	case "ascii":
		var b strings.Builder
		for _, c := range data {
			b.WriteRune(rune(c & 0x7f))
		}
		return native.From(vm, b.String())
	case "utf16le", "ucs2", "ucs-2", "utf-16le":
		out, err := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder().Bytes(data)
		if err != nil {
			panic(native.TypeError(vm, "Failed to encode utf16le string"))
		}
		return native.From(vm, string(out))
	default:
		panic(native.TypeError(vm, "%s", fmt.Sprintf("Unknown encoding: %s", encoding)))
	}
}

func latin1String(data []byte) string {
	var b strings.Builder
	b.Grow(len(data))
	for _, c := range data {
		b.WriteRune(rune(c))
	}
	return b.String()
}

// isArrayBufferView reports whether value is a Buffer, TypedArray or DataView.
func isArrayBufferView(vm *moejs.Runtime, value moejs.Value) bool {
	object := value.AsObject()
	ok := object != nil
	if !ok {
		return false
	}
	view := native.Get(vm, object, "buffer")
	if view.IsUndefined() {
		return false
	}
	isAB := view.IsObject() && (view.AsObject().Class() == engine.ClassArrayBuffer || view.AsObject().Class() == engine.ClassSharedArrayBuffer)
	return isAB
}

// viewBytes returns the raw bytes backing a Buffer, TypedArray or DataView.
// The returned slice shares memory with the JavaScript object, so writes are
// visible to the script.
func viewBytes(vm *moejs.Runtime, value moejs.Value) []byte {
	var data []byte
	if err := native.ExportTo(vm, value, &data); err != nil {
		invalidArgType(vm, "data", "an instance of Buffer, TypedArray, or DataView")
	}
	return data
}

// typedArrayInfo describes an ArrayBufferView for randomFill/getRandomValues.
type typedArrayInfo struct {
	raw      []byte
	elemSize int
	isView   bool // DataView: offset/size are bytes instead of elements
	kind     reflect.Kind
}

func typedArrayMeta(vm *moejs.Runtime, value moejs.Value) (typedArrayInfo, bool) {
	object := value.AsObject()
	ok := object != nil
	if !ok || !isArrayBufferView(vm, value) {
		return typedArrayInfo{}, false
	}
	raw := viewBytes(vm, value)
	meta := typedArrayInfo{raw: raw}
	if length := native.Get(vm, object, "length"); !length.IsUndefined() {
		elemCount := int(native.Int(vm, length))
		if elemCount > 0 {
			meta.elemSize = len(raw) / elemCount
		} else {
			meta.elemSize = 1
		}
		if exported, isTypedArray := object.TypedArrayElements(); isTypedArray {
			typ := reflect.TypeOf(exported)
			if typ != nil && typ.Kind() == reflect.Slice {
				meta.kind = typ.Elem().Kind()
			}
		}
	} else {
		meta.isView = true
		meta.elemSize = 1
	}
	return meta, true
}

// ---------------------------------------------------------------------------
// Hash and HMAC

type hashFactory func() hash.Hash

var hashFactories = map[string]hashFactory{
	"md5":        md5.New,
	"sha1":       sha1.New,
	"sha224":     sha256.New224,
	"sha256":     sha256.New,
	"sha384":     sha512.New384,
	"sha512":     sha512.New,
	"sha512-224": sha512.New512_224,
	"sha512-256": sha512.New512_256,
	"sha3-224":   sha3.New224,
	"sha3-256":   sha3.New256,
	"sha3-384":   sha3.New384,
	"sha3-512":   sha3.New512,
	"ripemd160":  ripemd160.New,
	"ripemd":     ripemd160.New,
	"rmd160":     ripemd160.New,
	"blake2b512": func() hash.Hash { h, _ := blake2b.New512(nil); return h },
	"blake2s256": func() hash.Hash { h, _ := blake2s.New256(nil); return h },
}

// normalizeHashName lowercases an algorithm name and accepts both the
// OpenSSL-style slash and the Node-style dash separators.
func normalizeHashName(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "/", "-")
}

func hashFactoryFor(name string) (hashFactory, bool) {
	factory, ok := hashFactories[normalizeHashName(name)]
	return factory, ok
}

func hashNames() []string {
	return []string{
		"blake2b512", "blake2s256", "md5", "ripemd160", "sha1", "sha224",
		"sha256", "sha3-224", "sha3-256", "sha3-384", "sha3-512", "sha384",
		"sha512", "sha512-224", "sha512-256",
	}
}

// digestState is the shared state of Hash and Hmac instances. Input is
// buffered so copy() works for every supported algorithm (including SHA-3
// variants that do not expose state marshaling).
type digestState struct {
	vm        *moejs.Runtime
	factory   hashFactory
	key       []byte
	data      []byte
	hmac      bool
	finalized bool
}

func newDigestState(vm *moejs.Runtime, algorithm string, key []byte, hmacMode bool) *digestState {
	factory, ok := hashFactoryFor(algorithm)
	if !ok {
		panic(native.TypeError(vm, "Digest method not supported"))
	}
	return &digestState{vm: vm, factory: factory, key: key, hmac: hmacMode}
}

func (m *Module) attachDigestMethods(vm *moejs.Runtime, object *engine.Object, state *digestState) {
	_ = native.Function(vm, object, "update", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_HASH_FINALIZED", "Digest already called")
		}
		state.data = append(state.data, m.decodeData(vm, moejs.Arg(args, 0), stringArg(vm, moejs.Arg(args, 1)))...)
		return this, nil
	})
	_ = native.Function(vm, object, "digest", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_HASH_FINALIZED", "Digest already called")
		}
		state.finalized = true
		return encodeOutput(vm, state.sum(), stringArg(vm, moejs.Arg(args, 0))), nil
	})
	if !state.hmac {
		_ = native.Function(vm, object, "copy", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
			if state.finalized {
				cryptoThrow(vm, "ERR_CRYPTO_HASH_FINALIZED", "Digest already called")
			}
			clone := &digestState{vm: state.vm, factory: state.factory, data: append([]byte(nil), state.data...)}
			object := vm.Realm().NewObject()
			m.attachDigestMethods(vm, object, clone)
			return engine.ObjectValue(object), nil
		})
	}
}

func (state *digestState) sum() []byte {
	if state.hmac {
		mac := hmac.New(state.factory, state.key)
		_, _ = mac.Write(state.data)
		return mac.Sum(nil)
	}
	digest := state.factory()
	_, _ = digest.Write(state.data)
	return digest.Sum(nil)
}

func (m *Module) createHash(vm *moejs.Runtime, args []moejs.Value, ctor moejs.Value) moejs.Value {
	object, err := native.New(vm, ctor, args...)
	if err != nil {
		panic(err)
	}
	return engine.ObjectValue(object)
}

func (m *Module) createHmac(vm *moejs.Runtime, args []moejs.Value, ctor moejs.Value) moejs.Value {
	object, err := native.New(vm, ctor, args...)
	if err != nil {
		panic(err)
	}
	return engine.ObjectValue(object)
}

// ---------------------------------------------------------------------------
// Random

func (m *Module) randomBytes(vm *moejs.Runtime, args []moejs.Value) moejs.Value {
	size := int(native.Int(vm, moejs.Arg(args, 0)))
	if size < 0 || size > 0x7fffffff {
		outOfRange(vm, "size", fmt.Sprintf("It must be >= 0 && <= 2147483647. Received %d", size))
	}
	if callback, ok := callable(moejs.Arg(args, 1)); ok {
		m.runAsync("randomBytes", func() (any, error) {
			data := make([]byte, size)
			_, err := rand.Read(data)
			return data, err
		}, func(vm *moejs.Runtime, result any) []moejs.Value {
			return []moejs.Value{engine.ObjectValue(buffer.WrapBytes(vm, result.([]byte)))}
		}, callback)
		return moejs.Undefined()
	}
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		panic(native.GoError(vm, err))
	}
	return engine.ObjectValue(buffer.WrapBytes(vm, data))
}

func (m *Module) randomFill(vm *moejs.Runtime, args []moejs.Value, sync bool) moejs.Value {
	arguments := append([]moejs.Value(nil), args...)
	callback := moejs.Undefined()
	if !sync && len(arguments) > 0 {
		if fn, ok := callable(arguments[len(arguments)-1]); ok {
			callback = fn
			arguments = arguments[:len(arguments)-1]
		}
	}
	bufferValue := argAt(arguments, 0)
	meta, ok := typedArrayMeta(vm, bufferValue)
	if !ok {
		invalidArgType(vm, "buf", "an instance of ArrayBuffer or ArrayBufferView")
	}
	offset := int(native.Int(vm, argAt(arguments, 1)))
	size := int(native.Int(vm, argAt(arguments, 2)))
	length := len(meta.raw) / meta.elemSize
	if offset < 0 {
		outOfRange(vm, "offset", fmt.Sprintf("It must be >= 0. Received %d", offset))
	}
	if size < 0 {
		outOfRange(vm, "size", fmt.Sprintf("It must be >= 0. Received %d", size))
	}
	if size == 0 {
		size = length - offset
	}
	if offset > length || size < 0 || size > length-offset {
		outOfRange(vm, "size + offset", fmt.Sprintf("It must be <= %d. Received %d", length, offset+size))
	}
	byteOffset := offset * meta.elemSize
	byteCount := size * meta.elemSize
	if sync {
		if _, err := rand.Read(meta.raw[byteOffset : byteOffset+byteCount]); err != nil {
			panic(native.GoError(vm, err))
		}
		return bufferValue
	}
	if !engine.IsCallable(callback) {
		invalidArgType(vm, "callback", "a function")
	}
	// BufferData is only valid until JavaScript next runs. The worker must
	// never retain or touch it: generate independently, then reacquire the
	// target on the loop after any intervening detach or resize.
	backing := native.Get(vm, bufferValue.AsObject(), "buffer").AsObject()
	m.runAsyncResult("randomFill", func() (any, error) {
		data := make([]byte, byteCount)
		_, err := rand.Read(data)
		return data, err
	}, func(vm *moejs.Runtime, result any) ([]moejs.Value, error) {
		if backing == nil || backing.IsDetachedBuffer() {
			return nil, newCryptoError("ERR_INVALID_STATE", "Cannot fill a detached ArrayBuffer")
		}
		raw, ok := bufferValue.AsObject().BufferData()
		if !ok || byteOffset > len(raw) || byteCount > len(raw)-byteOffset {
			return nil, newCryptoError("ERR_OUT_OF_RANGE", "randomFill target was resized out of bounds")
		}
		copy(raw[byteOffset:byteOffset+byteCount], result.([]byte))
		return []moejs.Value{bufferValue}, nil
	}, callback)
	return moejs.Undefined()
}

func (m *Module) randomInt(vm *moejs.Runtime, args []moejs.Value) moejs.Value {
	arguments := append([]moejs.Value(nil), args...)
	callback := moejs.Undefined()
	if len(arguments) > 0 {
		if fn, ok := callable(arguments[len(arguments)-1]); ok {
			callback = fn
			arguments = arguments[:len(arguments)-1]
		}
	}
	var min, max int64
	switch len(arguments) {
	case 1:
		max = safeInt(vm, arguments[0], "max")
	case 2:
		min = safeInt(vm, arguments[0], "min")
		max = safeInt(vm, arguments[1], "max")
	default:
		invalidArgType(vm, "max", "a safe integer")
	}
	if max > 281474976710655 {
		outOfRange(vm, "max", fmt.Sprintf("It must be <= 281474976710655. Received %d", max))
	}
	if max <= min {
		outOfRange(vm, "max", fmt.Sprintf("It must be greater than min (%d). Received %d", min, max))
	}
	run := func() (any, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(max-min))
		if err != nil {
			return nil, err
		}
		return n.Int64() + min, nil
	}
	if engine.IsCallable(callback) {
		m.runAsync("randomInt", run, func(vm *moejs.Runtime, result any) []moejs.Value {
			return []moejs.Value{native.From(vm, result.(int64))}
		}, callback)
		return moejs.Undefined()
	}
	result, err := run()
	if err != nil {
		panic(native.GoError(vm, err))
	}
	return native.From(vm, result.(int64))
}

func safeInt(vm *moejs.Runtime, value moejs.Value, name string) int64 {
	exported := native.Export(vm, value)
	switch n := exported.(type) {
	case int64:
		return n
	case float64:
		if math.Trunc(n) == n && n >= -9007199254740991 && n <= 9007199254740991 {
			return int64(n)
		}
	}
	panic(native.TypeError(vm, "%s", fmt.Sprintf("The %q argument must be a safe integer. Received %s", name, native.String(vm, value))))
}

func (m *Module) randomUUID(vm *moejs.Runtime, args []moejs.Value) moejs.Value {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(native.GoError(vm, err))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return native.From(vm, fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

func (m *Module) getRandomValues(vm *moejs.Runtime, args []moejs.Value) moejs.Value {
	value := moejs.Arg(args, 0)
	meta, ok := typedArrayMeta(vm, value)
	if !ok || meta.isView {
		panic(native.TypeError(vm, "The \"typedArray\" argument must be an instance of an integer-typed TypedArray"))
	}
	switch meta.kind {
	case reflect.Int8, reflect.Uint8, reflect.Int16, reflect.Uint16, reflect.Int32, reflect.Uint32, reflect.Int64, reflect.Uint64:
	default:
		panic(native.TypeError(vm, "The \"typedArray\" argument must be an instance of an integer-typed TypedArray"))
	}
	if len(meta.raw) > 65536 {
		object := native.GoError(vm, errors.New("getRandomValues() failed, entropy string limit reached"))
		_ = native.Set(vm, object.AsObject(), "name", "QuotaExceededError")
		_ = native.Set(vm, object.AsObject(), "code", 22)
		panic(object)
	}
	if _, err := rand.Read(meta.raw); err != nil {
		panic(native.GoError(vm, err))
	}
	return value
}

func (m *Module) timingSafeEqual(vm *moejs.Runtime, args []moejs.Value) moejs.Value {
	a := moejs.Arg(args, 0)
	b := moejs.Arg(args, 1)
	if !isArrayBufferView(vm, a) || !isArrayBufferView(vm, b) {
		invalidArgType(vm, "buf", "of type Buffer, TypedArray, or DataView")
	}
	aBytes := viewBytes(vm, a)
	bBytes := viewBytes(vm, b)
	if len(aBytes) != len(bBytes) {
		cryptoThrow(vm, "ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH", "Input buffers must have the same byte length")
	}
	return native.From(vm, subtle.ConstantTimeCompare(aBytes, bBytes) == 1)
}

// ---------------------------------------------------------------------------
// Key derivation

func (m *Module) pbkdf2(vm *moejs.Runtime, args []moejs.Value, sync bool) moejs.Value {
	password := m.decodeData(vm, moejs.Arg(args, 0), "")
	salt := m.decodeData(vm, moejs.Arg(args, 1), "")
	iterations := int(native.Int(vm, moejs.Arg(args, 2)))
	keylen := int(native.Int(vm, moejs.Arg(args, 3)))
	digestName := stringArg(vm, moejs.Arg(args, 4))
	if iterations < 1 || iterations > 0x7fffffff {
		outOfRange(vm, "iterations", fmt.Sprintf("It must be >= 1 && <= 2147483647. Received %d", iterations))
	}
	if keylen < 0 || keylen > 0x7fffffff {
		outOfRange(vm, "keylen", fmt.Sprintf("It must be >= 0 && <= 2147483647. Received %d", keylen))
	}
	factory, ok := hashFactoryFor(digestName)
	if !ok {
		cryptoThrow(vm, "ERR_CRYPTO_INVALID_DIGEST", "Invalid digest")
	}
	run := func() (any, error) {
		return pbkdf2.Key(factory, string(password), salt, iterations, keylen)
	}
	if !sync {
		callback, ok := callable(moejs.Arg(args, 5))
		if !ok {
			invalidArgType(vm, "callback", "a function")
		}
		m.runAsync("pbkdf2", run, func(vm *moejs.Runtime, result any) []moejs.Value {
			return []moejs.Value{engine.ObjectValue(buffer.WrapBytes(vm, result.([]byte)))}
		}, callback)
		return moejs.Undefined()
	}
	result, err := run()
	if err != nil {
		panic(native.GoError(vm, err))
	}
	return engine.ObjectValue(buffer.WrapBytes(vm, result.([]byte)))
}

type scryptOptions struct {
	n, r, p int
	maxmem  int
}

func (m *Module) scrypt(vm *moejs.Runtime, args []moejs.Value, sync bool) moejs.Value {
	password := m.decodeData(vm, moejs.Arg(args, 0), "")
	salt := m.decodeData(vm, moejs.Arg(args, 1), "")
	keylen := int(native.Int(vm, moejs.Arg(args, 2)))
	if keylen < 0 || keylen > 0x7fffffff {
		outOfRange(vm, "keylen", fmt.Sprintf("It must be >= 0 && <= 2147483647. Received %d", keylen))
	}
	options := scryptOptions{n: 16384, r: 8, p: 1, maxmem: 32 * 1024 * 1024}
	if object := moejs.Arg(args, 3).AsObject(); object != nil {
		options.n = scryptIntOption(vm, object, "N", "cost", options.n)
		options.r = scryptIntOption(vm, object, "r", "blockSize", options.r)
		options.p = scryptIntOption(vm, object, "p", "parallelization", options.p)
		options.maxmem = scryptIntOption(vm, object, "maxmem", "", options.maxmem)
	}
	if options.n <= 1 || options.n&(options.n-1) != 0 {
		cryptoThrow(vm, "ERR_CRYPTO_INVALID_SCRYPT_PARAMS", "Invalid scrypt params")
	}
	if options.r <= 0 || options.p <= 0 {
		cryptoThrow(vm, "ERR_CRYPTO_INVALID_SCRYPT_PARAMS", "Invalid scrypt params")
	}
	if uint64(options.n)*128*uint64(options.r) > uint64(options.maxmem) {
		cryptoThrow(vm, "ERR_CRYPTO_INVALID_SCRYPT_PARAMS", "Invalid scrypt params")
	}
	run := func() (any, error) {
		key, err := scrypt.Key(password, salt, options.n, options.r, options.p, keylen)
		if err != nil {
			return nil, newCryptoError("ERR_CRYPTO_INVALID_SCRYPT_PARAMS", "Invalid scrypt params")
		}
		return key, nil
	}
	if !sync {
		callback, ok := callable(moejs.Arg(args, 4))
		if !ok {
			callback, ok = callable(moejs.Arg(args, 3))
		}
		if !ok {
			invalidArgType(vm, "callback", "a function")
		}
		m.runAsync("scrypt", run, func(vm *moejs.Runtime, result any) []moejs.Value {
			return []moejs.Value{engine.ObjectValue(buffer.WrapBytes(vm, result.([]byte)))}
		}, callback)
		return moejs.Undefined()
	}
	result, err := run()
	if err != nil {
		panic(cryptoErrorObject(vm, err))
	}
	return engine.ObjectValue(buffer.WrapBytes(vm, result.([]byte)))
}

func scryptIntOption(vm *moejs.Runtime, object *engine.Object, name, alias string, fallback int) int {
	if value := native.Get(vm, object, name); !value.IsUndefined() && !value.IsNull() {
		return int(native.Int(vm, value))
	}
	if alias != "" {
		if value := native.Get(vm, object, alias); !value.IsUndefined() && !value.IsNull() {
			return int(native.Int(vm, value))
		}
	}
	return fallback
}

func (m *Module) hashOneShot(vm *moejs.Runtime, args []moejs.Value) moejs.Value {
	factory, ok := hashFactoryFor(stringArg(vm, moejs.Arg(args, 0)))
	if !ok {
		panic(native.TypeError(vm, "Digest method not supported"))
	}
	data := m.decodeData(vm, moejs.Arg(args, 1), "")
	digest := factory()
	_, _ = digest.Write(data)
	return encodeOutput(vm, digest.Sum(nil), stringArg(vm, moejs.Arg(args, 2)))
}

// ---------------------------------------------------------------------------
// Async callbacks

func (m *Module) runAsync(name string, run func() (any, error), values func(*moejs.Runtime, any) []moejs.Value, callback moejs.Value) {
	m.runAsyncResult(name, run, func(vm *moejs.Runtime, result any) ([]moejs.Value, error) {
		return values(vm, result), nil
	}, callback)
}

// runAsyncResult permits loop-side validation to fail through the callback,
// rather than panicking before the callback can be delivered.
func (m *Module) runAsyncResult(name string, run func() (any, error), values func(*moejs.Runtime, any) ([]moejs.Value, error), callback moejs.Value) {
	go func() {
		result, err := run()
		if !m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
			_ = m.runtime.RunJob(vm, "crypto."+name, func() error {
				var resultValues []moejs.Value
				if err == nil {
					resultValues, err = values(vm, result)
				}
				if err != nil {
					_, callbackErr := native.Call(vm, callback, moejs.Undefined(), cryptoErrorObject(vm, err))
					return callbackErr
				}
				callbackArgs := []moejs.Value{moejs.Null()}
				callbackArgs = append(callbackArgs, resultValues...)
				_, callbackErr := native.Call(vm, callback, moejs.Undefined(), callbackArgs...)
				return callbackErr
			})
		}) {
			return
		}
	}()
}

// ---------------------------------------------------------------------------
// Symmetric ciphers

type cipherKind uint8

const (
	cipherCBC cipherKind = iota
	cipherCTR
	cipherECB
	cipherGCM
	cipherChaCha
)

type cipherSpec struct {
	kind   cipherKind
	keyLen int
	ivLen  int // -1: no IV, 0: any length >= 1, positive: exact length
	tagLen int // default authentication tag length
}

var cipherSpecs = map[string]cipherSpec{
	"aes-128-cbc":       {cipherCBC, 16, 16, 0},
	"aes-192-cbc":       {cipherCBC, 24, 16, 0},
	"aes-256-cbc":       {cipherCBC, 32, 16, 0},
	"aes-128-ctr":       {cipherCTR, 16, 16, 0},
	"aes-192-ctr":       {cipherCTR, 24, 16, 0},
	"aes-256-ctr":       {cipherCTR, 32, 16, 0},
	"aes-128-ecb":       {cipherECB, 16, -1, 0},
	"aes-192-ecb":       {cipherECB, 24, -1, 0},
	"aes-256-ecb":       {cipherECB, 32, -1, 0},
	"aes-128-gcm":       {cipherGCM, 16, 0, 16},
	"aes-192-gcm":       {cipherGCM, 24, 0, 16},
	"aes-256-gcm":       {cipherGCM, 32, 0, 16},
	"chacha20-poly1305": {cipherChaCha, 32, 12, 16},
	"aes128":            {cipherCBC, 16, 16, 0},
	"aes192":            {cipherCBC, 24, 16, 0},
	"aes256":            {cipherCBC, 32, 16, 0},
}

func parseCipher(name string) (cipherSpec, bool) {
	spec, ok := cipherSpecs[strings.ToLower(strings.TrimSpace(name))]
	return spec, ok
}

func cipherNames() []string {
	return []string{
		"aes-128-cbc", "aes-128-ctr", "aes-128-ecb", "aes-128-gcm",
		"aes-192-cbc", "aes-192-ctr", "aes-192-ecb", "aes-192-gcm",
		"aes-256-cbc", "aes-256-ctr", "aes-256-ecb", "aes-256-gcm",
		"chacha20-poly1305",
	}
}

// ecbMode implements the ECB block mode (each block encrypted independently).
type ecbMode struct {
	block   cipher.Block
	encrypt bool
}

func (e *ecbMode) BlockSize() int { return e.block.BlockSize() }

func (e *ecbMode) CryptBlocks(dst, src []byte) {
	blockSize := e.block.BlockSize()
	for i := 0; i < len(src); i += blockSize {
		if e.encrypt {
			e.block.Encrypt(dst[i:i+blockSize], src[i:i+blockSize])
		} else {
			e.block.Decrypt(dst[i:i+blockSize], src[i:i+blockSize])
		}
	}
}

type cipherState struct {
	vm          *moejs.Runtime
	spec        cipherSpec
	encrypt     bool
	block       cipher.Block
	blockMode   cipher.BlockMode
	stream      cipher.Stream
	aead        cipher.AEAD
	nonce       []byte
	autoPadding bool
	pending     []byte
	buffered    []byte
	aad         []byte
	authTag     []byte
	authTagSet  bool
	tagLen      int
	finalized   bool
}

func (m *Module) makeCipherConstructor(vm *moejs.Runtime, encrypt bool) moejs.Value {
	return constructor(vm, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		state := m.newCipherState(vm, encrypt, args)
		m.attachCipherMethods(vm, this.AsObject(), state)
		return this, nil
	})
}

func (m *Module) newCipherState(vm *moejs.Runtime, encrypt bool, arguments []moejs.Value) *cipherState {
	spec, ok := parseCipher(stringArg(vm, argAt(arguments, 0)))
	if !ok {
		cryptoThrow(vm, "ERR_CRYPTO_UNKNOWN_CIPHER", "Unknown cipher")
	}
	key := m.decodeData(vm, argAt(arguments, 1), "")
	if len(key) != spec.keyLen {
		cryptoThrow(vm, "ERR_CRYPTO_INVALID_KEYLEN", "Invalid key length")
	}
	ivValue := argAt(arguments, 2)
	var iv []byte
	if !ivValue.IsUndefined() && !ivValue.IsNull() {
		iv = m.decodeData(vm, ivValue, "")
	}
	if spec.ivLen == -1 {
		if len(iv) != 0 {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_IV", "Invalid initialization vector")
		}
	} else if spec.ivLen > 0 {
		if len(iv) != spec.ivLen {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_IV", "Invalid initialization vector")
		}
	} else if len(iv) < 1 {
		cryptoThrow(vm, "ERR_CRYPTO_INVALID_IV", "Invalid initialization vector")
	}
	tagLen := spec.tagLen
	if options := argAt(arguments, 3).AsObject(); options != nil {
		if value := native.Get(vm, options, "authTagLength"); !value.IsUndefined() && !value.IsNull() {
			tagLen = int(native.Int(vm, value))
		}
	}
	if spec.tagLen > 0 && (tagLen < 12 || tagLen > 16) {
		cryptoThrow(vm, "ERR_CRYPTO_INVALID_AUTH_TAG", fmt.Sprintf("Invalid authentication tag length: %d", tagLen))
	}
	state := &cipherState{
		vm:          vm,
		spec:        spec,
		encrypt:     encrypt,
		autoPadding: true,
		nonce:       iv,
		tagLen:      tagLen,
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(native.GoError(vm, err))
	}
	state.block = block
	switch spec.kind {
	case cipherCBC:
		if encrypt {
			state.blockMode = cipher.NewCBCEncrypter(block, iv)
		} else {
			state.blockMode = cipher.NewCBCDecrypter(block, iv)
		}
	case cipherCTR:
		state.stream = cipher.NewCTR(block, iv)
	case cipherECB:
		state.blockMode = &ecbMode{block: block, encrypt: encrypt}
	case cipherGCM:
		if len(iv) == 12 {
			aead, gcmErr := cipher.NewGCMWithTagSize(block, tagLen)
			if gcmErr != nil {
				panic(native.GoError(vm, gcmErr))
			}
			state.aead = aead
		} else {
			if tagLen != 16 {
				cryptoThrow(vm, "ERR_CRYPTO_INVALID_AUTH_TAG", "Invalid authentication tag length: authTagLength with a non-12-byte IV is not supported")
			}
			aead, gcmErr := cipher.NewGCMWithNonceSize(block, len(iv))
			if gcmErr != nil {
				panic(native.GoError(vm, gcmErr))
			}
			state.aead = aead
		}
	case cipherChaCha:
		if tagLen != 16 {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_AUTH_TAG", fmt.Sprintf("Invalid authentication tag length: %d", tagLen))
		}
		aead, chachaErr := chacha20poly1305.New(key)
		if chachaErr != nil {
			panic(native.GoError(vm, chachaErr))
		}
		state.aead = aead
	}
	return state
}

func (m *Module) attachCipherMethods(vm *moejs.Runtime, object *engine.Object, state *cipherState) {
	_ = native.Function(vm, object, "update", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation update")
		}
		data := m.decodeData(vm, moejs.Arg(args, 0), stringArg(vm, moejs.Arg(args, 1)))
		var out []byte
		switch state.spec.kind {
		case cipherCBC, cipherECB:
			state.pending = append(state.pending, data...)
			blockSize := state.block.BlockSize()
			n := len(state.pending) / blockSize * blockSize
			if !state.encrypt && state.autoPadding && n > 0 {
				// Hold the last full block back so final() can strip padding.
				n -= blockSize
			}
			if n > 0 {
				out = make([]byte, n)
				state.blockMode.CryptBlocks(out, state.pending[:n])
				state.pending = state.pending[n:]
			}
		case cipherCTR:
			out = make([]byte, len(data))
			state.stream.XORKeyStream(out, data)
		case cipherGCM, cipherChaCha:
			// Go's AEAD API is all-or-nothing; data is buffered and returned
			// from final(). Buffer.concat([update(), final()]) code still works.
			state.buffered = append(state.buffered, data...)
		}
		return encodeOutput(vm, out, stringArg(vm, moejs.Arg(args, 2))), nil
	})
	_ = native.Function(vm, object, "final", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation final")
		}
		state.finalized = true
		var out []byte
		switch state.spec.kind {
		case cipherCBC, cipherECB:
			blockSize := state.block.BlockSize()
			if state.encrypt {
				if state.autoPadding {
					pad := blockSize - len(state.pending)%blockSize
					for i := 0; i < pad; i++ {
						state.pending = append(state.pending, byte(pad))
					}
				} else if len(state.pending)%blockSize != 0 {
					cryptoThrow(vm, "", "error:1C80006B:Provider routines::wrong final block length")
				}
			} else if len(state.pending)%blockSize != 0 || (state.autoPadding && len(state.pending) == 0) {
				cryptoThrow(vm, "", "error:1C80006B:Provider routines::wrong final block length")
			}
			if len(state.pending) > 0 {
				out = make([]byte, len(state.pending))
				state.blockMode.CryptBlocks(out, state.pending)
			}
			if !state.encrypt && state.autoPadding && len(out) > 0 {
				pad := int(out[len(out)-1])
				if pad < 1 || pad > blockSize || pad > len(out) {
					cryptoThrow(vm, "", "error:1C80006B:Provider routines::wrong final block length")
				}
				for i := 1; i <= pad; i++ {
					if out[len(out)-i] != byte(pad) {
						cryptoThrow(vm, "", "error:1C80006B:Provider routines::wrong final block length")
					}
				}
				out = out[:len(out)-pad]
			}
		case cipherCTR:
			// Stream ciphers emit everything from update(); final() is empty.
		case cipherGCM, cipherChaCha:
			if state.encrypt {
				sealed := state.aead.Seal(nil, state.nonce, state.buffered, state.aad)
				state.authTag = append([]byte(nil), sealed[len(sealed)-state.tagLen:]...)
				// Match Node's data flow: ciphertext comes from update()/final()
				// and the authentication tag only from getAuthTag().
				out = sealed[:len(sealed)-state.tagLen]
			} else {
				if !state.authTagSet {
					panic(native.GoError(vm, errors.New("Unsupported state or unable to authenticate data")))
				}
				plain, err := state.aead.Open(nil, state.nonce, append(state.buffered, state.authTag...), state.aad)
				if err != nil {
					panic(native.GoError(vm, errors.New("Unsupported state or unable to authenticate data")))
				}
				out = plain
			}
		}
		return encodeOutput(vm, out, stringArg(vm, moejs.Arg(args, 0))), nil
	})
	_ = native.Function(vm, object, "setAutoPadding", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if state.spec.kind != cipherCBC && state.spec.kind != cipherECB {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAutoPadding")
		}
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAutoPadding")
		}
		autoPadding := true
		if value := moejs.Arg(args, 0); !value.IsUndefined() {
			autoPadding = native.Bool(value)
		}
		state.autoPadding = autoPadding
		return this, nil
	})
	_ = native.Function(vm, object, "setAAD", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if state.spec.kind != cipherGCM && state.spec.kind != cipherChaCha {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAAD")
		}
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAAD")
		}
		value := moejs.Arg(args, 0)
		if !isArrayBufferView(vm, value) {
			invalidArgType(vm, "aad", "an instance of Buffer, TypedArray, or DataView")
		}
		state.aad = append(state.aad, viewBytes(vm, value)...)
		return this, nil
	})
	_ = native.Function(vm, object, "setAuthTag", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if state.spec.kind != cipherGCM && state.spec.kind != cipherChaCha {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAuthTag")
		}
		if state.encrypt || state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAuthTag")
		}
		value := moejs.Arg(args, 0)
		if !isArrayBufferView(vm, value) {
			invalidArgType(vm, "tag", "an instance of Buffer, TypedArray, or DataView")
		}
		tag := viewBytes(vm, value)
		if len(tag) != state.tagLen {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_AUTH_TAG", fmt.Sprintf("Invalid authentication tag length: %d", len(tag)))
		}
		state.authTag = append([]byte(nil), tag...)
		state.authTagSet = true
		return this, nil
	})
	if state.encrypt && (state.spec.kind == cipherGCM || state.spec.kind == cipherChaCha) {
		_ = native.Function(vm, object, "getAuthTag", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
			if !state.finalized {
				cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation getAuthTag")
			}
			return engine.ObjectValue(buffer.WrapBytes(vm, state.authTag)), nil
		})
	}
}

func (m *Module) createCipheriv(vm *moejs.Runtime, args []moejs.Value, ctor moejs.Value) moejs.Value {
	object, err := native.New(vm, ctor, args...)
	if err != nil {
		panic(err)
	}
	return engine.ObjectValue(object)
}

// ---------------------------------------------------------------------------
// Sign and verify

type signScheme uint8

const (
	schemeAuto signScheme = iota // dispatch on key type at operation time
	schemeRSA
	schemeECDSA
	schemeEd25519
)

type signAlgo struct {
	hash   crypto.Hash
	scheme signScheme
}

var signHashes = map[string]crypto.Hash{
	"md5":        crypto.MD5,
	"sha1":       crypto.SHA1,
	"sha224":     crypto.SHA224,
	"sha256":     crypto.SHA256,
	"sha384":     crypto.SHA384,
	"sha512":     crypto.SHA512,
	"sha512-224": crypto.SHA512_224,
	"sha512-256": crypto.SHA512_256,
	"sha3-224":   crypto.SHA3_224,
	"sha3-256":   crypto.SHA3_256,
	"sha3-384":   crypto.SHA3_384,
	"sha3-512":   crypto.SHA3_512,
	"ripemd160":  crypto.RIPEMD160,
	"blake2b512": crypto.BLAKE2b_512,
	"blake2s256": crypto.BLAKE2s_256,
}

func parseSignAlgorithm(name string) (signAlgo, error) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return signAlgo{}, newCryptoError("ERR_CRYPTO_INVALID_DIGEST", "Invalid digest")
	}
	if normalized == "ed25519" {
		return signAlgo{scheme: schemeEd25519}, nil
	}
	scheme := schemeAuto
	switch {
	case strings.HasPrefix(normalized, "rsa-"):
		scheme = schemeRSA
		normalized = strings.TrimPrefix(normalized, "rsa-")
	case strings.HasPrefix(normalized, "ecdsa-with-"):
		scheme = schemeECDSA
		normalized = strings.TrimPrefix(normalized, "ecdsa-with-")
	case strings.HasPrefix(normalized, "ecdsa-"):
		scheme = schemeECDSA
		normalized = strings.TrimPrefix(normalized, "ecdsa-")
	}
	hashID, ok := signHashes[normalizeHashName(normalized)]
	if !ok {
		return signAlgo{}, newCryptoError("ERR_CRYPTO_INVALID_DIGEST", "Invalid digest")
	}
	return signAlgo{hash: hashID, scheme: scheme}, nil
}

func hashData(algo signAlgo, data []byte) []byte {
	digest := algo.hash.New()
	_, _ = digest.Write(data)
	return digest.Sum(nil)
}

func parsePrivateKey(keyData []byte) (any, error) {
	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, newCryptoError("ERR_OSSL_PEM_NO_START_LINE", "error:0909006C:PEM routines:get_name:no start line")
	}
	if privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return privateKey, nil
	}
	if privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return privateKey, nil
	}
	if privateKey, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return privateKey, nil
	}
	return nil, newCryptoError("ERR_OSSL_UNSUPPORTED", "error:1E08010C:DECODER routines::unsupported")
}

func publicFromPrivate(privateKey any) any {
	switch key := privateKey.(type) {
	case *rsa.PrivateKey:
		return &key.PublicKey
	case *ecdsa.PrivateKey:
		return &key.PublicKey
	case ed25519.PrivateKey:
		return key.Public()
	default:
		return nil
	}
}

func parsePublicKey(keyData []byte) (any, error) {
	block, _ := pem.Decode(keyData)
	if block != nil {
		if publicKey, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
			return publicKey, nil
		}
		if publicKey, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
			return publicKey, nil
		}
	}
	// Node also accepts private keys where a public key is expected.
	privateKey, err := parsePrivateKey(keyData)
	if err != nil {
		return nil, err
	}
	publicKey := publicFromPrivate(privateKey)
	if publicKey == nil {
		return nil, newCryptoError("ERR_OSSL_UNSUPPORTED", "error:1E08010C:DECODER routines::unsupported")
	}
	return publicKey, nil
}

var errInvalidDigestForKey = newCryptoError("ERR_OSSL_INVALID_DIGEST", "error:1C80007A:Provider routines::invalid digest")

func computeSignature(algo signAlgo, key any, data []byte) ([]byte, error) {
	switch privateKey := key.(type) {
	case *rsa.PrivateKey:
		if algo.scheme == schemeEd25519 {
			return nil, errInvalidDigestForKey
		}
		return rsa.SignPKCS1v15(rand.Reader, privateKey, algo.hash, hashData(algo, data))
	case *ecdsa.PrivateKey:
		if algo.scheme == schemeEd25519 {
			return nil, errInvalidDigestForKey
		}
		return ecdsa.SignASN1(rand.Reader, privateKey, hashData(algo, data))
	case ed25519.PrivateKey:
		if algo.scheme != schemeEd25519 {
			return nil, errInvalidDigestForKey
		}
		return ed25519.Sign(privateKey, data), nil
	default:
		return nil, newCryptoError("ERR_OSSL_BAD_KEY", "error:02000066:rsa routines::bad key type")
	}
}

func verifySignature(algo signAlgo, key any, data, signature []byte) (bool, error) {
	switch publicKey := key.(type) {
	case *rsa.PublicKey:
		if algo.scheme == schemeEd25519 {
			return false, errInvalidDigestForKey
		}
		return rsa.VerifyPKCS1v15(publicKey, algo.hash, hashData(algo, data), signature) == nil, nil
	case *ecdsa.PublicKey:
		if algo.scheme == schemeEd25519 {
			return false, errInvalidDigestForKey
		}
		return ecdsa.VerifyASN1(publicKey, hashData(algo, data), signature), nil
	case ed25519.PublicKey:
		if algo.scheme != schemeEd25519 {
			return false, errInvalidDigestForKey
		}
		return ed25519.Verify(publicKey, data, signature), nil
	default:
		return false, newCryptoError("ERR_OSSL_BAD_KEY", "error:02000066:rsa routines::bad key type")
	}
}

type signState struct {
	vm        *moejs.Runtime
	algo      signAlgo
	data      []byte
	isSign    bool
	finalized bool
}

func (m *Module) makeSignConstructor(vm *moejs.Runtime, isSign bool) moejs.Value {
	return constructor(vm, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if isUndefined(moejs.Arg(args, 0)) || isNull(moejs.Arg(args, 0)) {
			invalidArgType(vm, "algorithm", "of type string")
		}
		algo, err := parseSignAlgorithm(stringArg(vm, moejs.Arg(args, 0)))
		if err != nil {
			panic(cryptoErrorObject(vm, err))
		}
		state := &signState{vm: vm, algo: algo, isSign: isSign}
		_ = native.Function(vm, this.AsObject(), "update", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
			if state.finalized {
				cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation update")
			}
			state.data = append(state.data, m.decodeData(vm, moejs.Arg(args, 0), stringArg(vm, moejs.Arg(args, 1)))...)
			return this, nil
		})
		if isSign {
			_ = native.Function(vm, this.AsObject(), "sign", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
				if state.finalized {
					cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation sign")
				}
				state.finalized = true
				keyBytes := m.decodeData(vm, moejs.Arg(args, 0), "")
				privateKey, parseErr := parsePrivateKey(keyBytes)
				if parseErr != nil {
					panic(cryptoErrorObject(vm, parseErr))
				}
				signature, signErr := computeSignature(state.algo, privateKey, state.data)
				if signErr != nil {
					panic(cryptoErrorObject(vm, signErr))
				}
				return encodeOutput(vm, signature, stringArg(vm, moejs.Arg(args, 1))), nil
			})
		} else {
			_ = native.Function(vm, this.AsObject(), "verify", func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
				if state.finalized {
					cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation verify")
				}
				state.finalized = true
				keyBytes := m.decodeData(vm, moejs.Arg(args, 0), "")
				signature := m.decodeData(vm, moejs.Arg(args, 1), stringArg(vm, moejs.Arg(args, 2)))
				publicKey, parseErr := parsePublicKey(keyBytes)
				if parseErr != nil {
					panic(cryptoErrorObject(vm, parseErr))
				}
				valid, verifyErr := verifySignature(state.algo, publicKey, state.data, signature)
				if verifyErr != nil {
					panic(cryptoErrorObject(vm, verifyErr))
				}
				return native.From(vm, valid), nil
			})
		}
		return this, nil
	})
}

func (m *Module) createSign(vm *moejs.Runtime, args []moejs.Value, ctor moejs.Value) moejs.Value {
	object, err := native.New(vm, ctor, args...)
	if err != nil {
		panic(err)
	}
	return engine.ObjectValue(object)
}

func (m *Module) signOneShot(vm *moejs.Runtime, args []moejs.Value, isSign bool) moejs.Value {
	algorithmValue := moejs.Arg(args, 0)
	algorithmName := stringArg(vm, algorithmValue)
	if algorithmValue.IsNull() {
		algorithmName = "ed25519"
	}
	algo, err := parseSignAlgorithm(algorithmName)
	if err != nil {
		panic(cryptoErrorObject(vm, err))
	}
	data := m.decodeData(vm, moejs.Arg(args, 1), "")
	keyBytes := m.decodeData(vm, moejs.Arg(args, 2), "")
	var run func() (any, error)
	if isSign {
		run = func() (any, error) {
			privateKey, parseErr := parsePrivateKey(keyBytes)
			if parseErr != nil {
				return nil, parseErr
			}
			return computeSignature(algo, privateKey, data)
		}
	} else {
		signature := m.decodeData(vm, moejs.Arg(args, 3), stringArg(vm, moejs.Arg(args, 4)))
		run = func() (any, error) {
			publicKey, parseErr := parsePublicKey(keyBytes)
			if parseErr != nil {
				return nil, parseErr
			}
			return verifySignature(algo, publicKey, data, signature)
		}
	}
	if isSign {
		if callback, ok := callable(moejs.Arg(args, 3)); ok {
			m.runAsync("sign", run, func(vm *moejs.Runtime, result any) []moejs.Value {
				return []moejs.Value{engine.ObjectValue(buffer.WrapBytes(vm, result.([]byte)))}
			}, callback)
			return moejs.Undefined()
		}
	} else if callback, ok := callable(moejs.Arg(args, 4)); ok {
		m.runAsync("verify", run, func(vm *moejs.Runtime, result any) []moejs.Value {
			return []moejs.Value{native.From(vm, result.(bool))}
		}, callback)
		return moejs.Undefined()
	}
	result, err := run()
	if err != nil {
		panic(cryptoErrorObject(vm, err))
	}
	if isSign {
		return engine.ObjectValue(buffer.WrapBytes(vm, result.([]byte)))
	}
	return native.From(vm, result.(bool))
}

// callable keeps callbacks as raw MoeJS values.
func callable(v moejs.Value) (moejs.Value, bool) { return v, engine.IsCallable(v) }
func isUndefined(v moejs.Value) bool             { return v.IsUndefined() }
func isNull(v moejs.Value) bool                  { return v.IsNull() }

// constructor uses the engine's explicit construct hook, not reflected binding.
func constructor(vm *moejs.Runtime, fn moejs.NativeFunc) moejs.Value {
	call := native.Bind(vm, "", 0, fn)
	var ctor *engine.Object
	construct := func(r *moejs.Realm, args []moejs.Value, target *engine.Object) (moejs.Value, error) {
		object := r.NewObject()
		if target != nil {
			p := native.Get(vm, target, "prototype")
			if p.IsObject() {
				if !object.SetPrototypeOf(r, p.AsObject()) {
					return moejs.Undefined(), r.TypeError("invalid constructor prototype")
				}
			}
		}
		result, err := vm.CallFunction(call, engine.ObjectValue(object), args...)
		if err != nil {
			return moejs.Undefined(), err
		}
		if result.IsObject() {
			return result, nil
		}
		return engine.ObjectValue(object), nil
	}
	ctor = vm.Realm().NewNativeConstructor(engine.FromGoString(""), 0, func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return construct(r, args, ctor)
	}, construct)
	prototype := vm.Realm().NewObject()
	_ = native.Set(vm, prototype, "constructor", engine.ObjectValue(ctor))
	_ = ctor.DefinePropertyOrThrow(vm.Realm(), vm.Realm().KeyFromGoString("prototype"), engine.DataDescriptor(engine.ObjectValue(prototype), 1))
	return engine.ObjectValue(ctor)
}
