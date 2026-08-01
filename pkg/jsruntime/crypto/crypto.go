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

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
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
func (m *Module) Load(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()

	hashCtor := vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
		state := newDigestState(vm, stringArg(call.Argument(0)), nil, false)
		m.attachDigestMethods(vm, call.This, state)
		return nil
	})
	hmacCtor := vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
		state := newDigestState(vm, stringArg(call.Argument(0)), m.decodeData(vm, call.Argument(1), ""), true)
		m.attachDigestMethods(vm, call.This, state)
		return nil
	})
	signCtor := m.makeSignConstructor(vm, true)
	verifyCtor := m.makeSignConstructor(vm, false)
	cipherCtor := m.makeCipherConstructor(vm, true)
	decipherCtor := m.makeCipherConstructor(vm, false)

	_ = exports.Set("Hash", hashCtor)
	_ = exports.Set("Hmac", hmacCtor)
	_ = exports.Set("Sign", signCtor)
	_ = exports.Set("Verify", verifyCtor)
	_ = exports.Set("Cipher", cipherCtor)
	_ = exports.Set("Decipher", decipherCtor)

	_ = exports.Set("createHash", func(call goja.FunctionCall) goja.Value { return m.createHash(vm, call, hashCtor) })
	_ = exports.Set("createHmac", func(call goja.FunctionCall) goja.Value { return m.createHmac(vm, call, hmacCtor) })
	_ = exports.Set("createCipheriv", func(call goja.FunctionCall) goja.Value { return m.createCipheriv(vm, call, cipherCtor) })
	_ = exports.Set("createDecipheriv", func(call goja.FunctionCall) goja.Value { return m.createCipheriv(vm, call, decipherCtor) })
	_ = exports.Set("createSign", func(call goja.FunctionCall) goja.Value { return m.createSign(vm, call, signCtor) })
	_ = exports.Set("createVerify", func(call goja.FunctionCall) goja.Value { return m.createSign(vm, call, verifyCtor) })
	_ = exports.Set("sign", func(call goja.FunctionCall) goja.Value { return m.signOneShot(vm, call, true) })
	_ = exports.Set("verify", func(call goja.FunctionCall) goja.Value { return m.signOneShot(vm, call, false) })

	_ = exports.Set("randomBytes", func(call goja.FunctionCall) goja.Value { return m.randomBytes(vm, call) })
	_ = exports.Set("randomFillSync", func(call goja.FunctionCall) goja.Value { return m.randomFill(vm, call, true) })
	_ = exports.Set("randomFill", func(call goja.FunctionCall) goja.Value { return m.randomFill(vm, call, false) })
	_ = exports.Set("randomInt", func(call goja.FunctionCall) goja.Value { return m.randomInt(vm, call) })
	_ = exports.Set("randomUUID", func(call goja.FunctionCall) goja.Value { return m.randomUUID(vm, call) })
	_ = exports.Set("getRandomValues", func(call goja.FunctionCall) goja.Value { return m.getRandomValues(vm, call) })

	_ = exports.Set("timingSafeEqual", func(call goja.FunctionCall) goja.Value { return m.timingSafeEqual(vm, call) })

	_ = exports.Set("pbkdf2", func(call goja.FunctionCall) goja.Value { return m.pbkdf2(vm, call, false) })
	_ = exports.Set("pbkdf2Sync", func(call goja.FunctionCall) goja.Value { return m.pbkdf2(vm, call, true) })
	_ = exports.Set("scrypt", func(call goja.FunctionCall) goja.Value { return m.scrypt(vm, call, false) })
	_ = exports.Set("scryptSync", func(call goja.FunctionCall) goja.Value { return m.scrypt(vm, call, true) })

	_ = exports.Set("hash", func(call goja.FunctionCall) goja.Value { return m.hashOneShot(vm, call) })
	_ = exports.Set("getHashes", func(call goja.FunctionCall) goja.Value { return vm.ToValue(hashNames()) })
	_ = exports.Set("getCiphers", func(call goja.FunctionCall) goja.Value { return vm.ToValue(cipherNames()) })
	_ = exports.Set("constants", map[string]int{
		"RSA_PKCS1_PADDING":      1,
		"RSA_SSLV23_PADDING":     2,
		"RSA_NO_PADDING":         3,
		"RSA_PKCS1_OAEP_PADDING": 4,
		"RSA_X931_PADDING":       5,
		"RSA_PKCS1_PSS_PADDING":  6,
	})

	_ = module.Set("exports", exports)
}

// ---------------------------------------------------------------------------
// Small helpers

func argAt(arguments []goja.Value, index int) goja.Value {
	if index < 0 || index >= len(arguments) {
		return goja.Undefined()
	}
	return arguments[index]
}

func stringArg(value goja.Value) string {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	return value.String()
}

func callArg(call goja.FunctionCall, index int) goja.Value { return argAt(call.Arguments, index) }

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
func cryptoThrow(vm *goja.Runtime, code, message string) {
	panic(cryptoErrorObject(vm, newCryptoError(code, message)))
}

// cryptoErrorObject converts an error into a JavaScript Error, attaching the
// Node-style code property when the error carries one.
func cryptoErrorObject(vm *goja.Runtime, err error) *goja.Object {
	object := vm.NewGoError(err)
	var coded *cryptoError
	if errors.As(err, &coded) && coded.code != "" {
		_ = object.Set("code", coded.code)
	}
	return object
}

func invalidArgType(vm *goja.Runtime, name, expected string) {
	panic(vm.NewTypeError(fmt.Sprintf("The %q argument must be %s", name, expected)))
}

func outOfRange(vm *goja.Runtime, name, message string) {
	cryptoThrow(vm, "ERR_OUT_OF_RANGE", fmt.Sprintf("The value of %q is out of range. %s", name, message))
}

// ---------------------------------------------------------------------------
// Encodings

// decodeData converts a JavaScript value to bytes. Strings are decoded with
// the optional input encoding (utf8 by default); Buffer, TypedArray, DataView
// and ArrayBuffer values are taken as raw bytes.
func (m *Module) decodeData(vm *goja.Runtime, value goja.Value, encoding string) []byte {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		invalidArgType(vm, "data", "of type string or an instance of Buffer, TypedArray, or DataView")
	}
	if text, ok := value.Export().(string); ok {
		return decodeString(vm, text, encoding)
	}
	if !isArrayBufferView(value) {
		invalidArgType(vm, "data", "of type string or an instance of Buffer, TypedArray, or DataView")
	}
	return append([]byte(nil), viewBytes(vm, value)...)
}

func decodeString(vm *goja.Runtime, text, encoding string) []byte {
	switch strings.ToLower(encoding) {
	case "", "utf8", "utf-8":
		return []byte(text)
	case "hex":
		data, err := hex.DecodeString(text)
		if err != nil {
			panic(vm.NewTypeError("Invalid hex string"))
		}
		return data
	case "base64":
		data, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			if data, err = base64.RawStdEncoding.DecodeString(text); err != nil {
				panic(vm.NewTypeError("Invalid base64 string"))
			}
		}
		return data
	case "base64url":
		data, err := base64.RawURLEncoding.DecodeString(text)
		if err != nil {
			if data, err = base64.URLEncoding.DecodeString(text); err != nil {
				panic(vm.NewTypeError("Invalid base64url string"))
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
			panic(vm.NewTypeError("Invalid utf16le string"))
		}
		return out
	default:
		panic(vm.NewTypeError(fmt.Sprintf("Unknown encoding: %s", encoding)))
	}
}

// encodeOutput converts bytes to a Buffer (default) or an encoded string.
func encodeOutput(vm *goja.Runtime, data []byte, encoding string) goja.Value {
	if encoding == "" {
		return buffer.WrapBytes(vm, data)
	}
	switch strings.ToLower(encoding) {
	case "hex":
		return vm.ToValue(hex.EncodeToString(data))
	case "base64":
		return vm.ToValue(base64.StdEncoding.EncodeToString(data))
	case "base64url":
		return vm.ToValue(base64.RawURLEncoding.EncodeToString(data))
	case "utf8", "utf-8":
		return vm.ToValue(string(data))
	case "latin1", "binary":
		return vm.ToValue(latin1String(data))
	case "ascii":
		var b strings.Builder
		for _, c := range data {
			b.WriteRune(rune(c & 0x7f))
		}
		return vm.ToValue(b.String())
	case "utf16le", "ucs2", "ucs-2", "utf-16le":
		out, err := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder().Bytes(data)
		if err != nil {
			panic(vm.NewTypeError("Failed to encode utf16le string"))
		}
		return vm.ToValue(string(out))
	default:
		panic(vm.NewTypeError(fmt.Sprintf("Unknown encoding: %s", encoding)))
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
func isArrayBufferView(value goja.Value) bool {
	object, ok := value.(*goja.Object)
	if !ok {
		return false
	}
	view := object.Get("buffer")
	if view == nil || goja.IsUndefined(view) {
		return false
	}
	_, isAB := view.Export().(goja.ArrayBuffer)
	return isAB
}

// viewBytes returns the raw bytes backing a Buffer, TypedArray or DataView.
// The returned slice shares memory with the JavaScript object, so writes are
// visible to the script.
func viewBytes(vm *goja.Runtime, value goja.Value) []byte {
	var data []byte
	if err := vm.ExportTo(value, &data); err != nil {
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

func typedArrayMeta(vm *goja.Runtime, value goja.Value) (typedArrayInfo, bool) {
	object, ok := value.(*goja.Object)
	if !ok || !isArrayBufferView(value) {
		return typedArrayInfo{}, false
	}
	raw := viewBytes(vm, value)
	meta := typedArrayInfo{raw: raw}
	if length := object.Get("length"); length != nil && !goja.IsUndefined(length) {
		elemCount := int(length.ToInteger())
		if elemCount > 0 {
			meta.elemSize = len(raw) / elemCount
		} else {
			meta.elemSize = 1
		}
		if exported := object.Export(); exported != nil {
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
	vm        *goja.Runtime
	factory   hashFactory
	key       []byte
	data      []byte
	hmac      bool
	finalized bool
}

func newDigestState(vm *goja.Runtime, algorithm string, key []byte, hmacMode bool) *digestState {
	factory, ok := hashFactoryFor(algorithm)
	if !ok {
		panic(vm.NewTypeError("Digest method not supported"))
	}
	return &digestState{vm: vm, factory: factory, key: key, hmac: hmacMode}
}

func (m *Module) attachDigestMethods(vm *goja.Runtime, object *goja.Object, state *digestState) {
	_ = object.Set("update", func(call goja.FunctionCall) goja.Value {
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_HASH_FINALIZED", "Digest already called")
		}
		state.data = append(state.data, m.decodeData(vm, callArg(call, 0), stringArg(callArg(call, 1)))...)
		return call.This
	})
	_ = object.Set("digest", func(call goja.FunctionCall) goja.Value {
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_HASH_FINALIZED", "Digest already called")
		}
		state.finalized = true
		return encodeOutput(vm, state.sum(), stringArg(callArg(call, 0)))
	})
	if !state.hmac {
		_ = object.Set("copy", func(call goja.FunctionCall) goja.Value {
			if state.finalized {
				cryptoThrow(vm, "ERR_CRYPTO_HASH_FINALIZED", "Digest already called")
			}
			clone := &digestState{vm: state.vm, factory: state.factory, data: append([]byte(nil), state.data...)}
			object := vm.NewObject()
			m.attachDigestMethods(vm, object, clone)
			return object
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

func (m *Module) createHash(vm *goja.Runtime, call goja.FunctionCall, ctor goja.Value) goja.Value {
	object, err := vm.New(ctor, call.Arguments...)
	if err != nil {
		panic(err)
	}
	return object
}

func (m *Module) createHmac(vm *goja.Runtime, call goja.FunctionCall, ctor goja.Value) goja.Value {
	object, err := vm.New(ctor, call.Arguments...)
	if err != nil {
		panic(err)
	}
	return object
}

// ---------------------------------------------------------------------------
// Random

func (m *Module) randomBytes(vm *goja.Runtime, call goja.FunctionCall) goja.Value {
	size := int(callArg(call, 0).ToInteger())
	if size < 0 || size > 0x7fffffff {
		outOfRange(vm, "size", fmt.Sprintf("It must be >= 0 && <= 2147483647. Received %d", size))
	}
	if callback, ok := goja.AssertFunction(callArg(call, 1)); ok {
		m.runAsync("randomBytes", func() (any, error) {
			data := make([]byte, size)
			_, err := rand.Read(data)
			return data, err
		}, func(vm *goja.Runtime, result any) []goja.Value {
			return []goja.Value{buffer.WrapBytes(vm, result.([]byte))}
		}, callback)
		return goja.Undefined()
	}
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		panic(vm.NewGoError(err))
	}
	return buffer.WrapBytes(vm, data)
}

func (m *Module) randomFill(vm *goja.Runtime, call goja.FunctionCall, sync bool) goja.Value {
	arguments := append([]goja.Value(nil), call.Arguments...)
	var callback goja.Callable
	if !sync && len(arguments) > 0 {
		if fn, ok := goja.AssertFunction(arguments[len(arguments)-1]); ok {
			callback = fn
			arguments = arguments[:len(arguments)-1]
		}
	}
	bufferValue := argAt(arguments, 0)
	meta, ok := typedArrayMeta(vm, bufferValue)
	if !ok {
		invalidArgType(vm, "buf", "an instance of ArrayBuffer or ArrayBufferView")
	}
	offset := int(argAt(arguments, 1).ToInteger())
	size := int(argAt(arguments, 2).ToInteger())
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
	if offset < 0 || size < 0 || offset+size > length {
		outOfRange(vm, "size + offset", fmt.Sprintf("It must be <= %d. Received %d", length, offset+size))
	}
	byteOffset := offset * meta.elemSize
	byteCount := size * meta.elemSize
	if sync {
		if _, err := rand.Read(meta.raw[byteOffset : byteOffset+byteCount]); err != nil {
			panic(vm.NewGoError(err))
		}
		return bufferValue
	}
	m.runAsync("randomFill", func() (any, error) {
		_, err := rand.Read(meta.raw[byteOffset : byteOffset+byteCount])
		return bufferValue, err
	}, func(vm *goja.Runtime, result any) []goja.Value {
		return []goja.Value{result.(goja.Value)}
	}, callback)
	return goja.Undefined()
}

func (m *Module) randomInt(vm *goja.Runtime, call goja.FunctionCall) goja.Value {
	arguments := append([]goja.Value(nil), call.Arguments...)
	var callback goja.Callable
	if len(arguments) > 0 {
		if fn, ok := goja.AssertFunction(arguments[len(arguments)-1]); ok {
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
	if callback != nil {
		m.runAsync("randomInt", run, func(vm *goja.Runtime, result any) []goja.Value {
			return []goja.Value{vm.ToValue(result.(int64))}
		}, callback)
		return goja.Undefined()
	}
	result, err := run()
	if err != nil {
		panic(vm.NewGoError(err))
	}
	return vm.ToValue(result.(int64))
}

func safeInt(vm *goja.Runtime, value goja.Value, name string) int64 {
	exported := value.Export()
	switch n := exported.(type) {
	case int64:
		return n
	case float64:
		if math.Trunc(n) == n && n >= -9007199254740991 && n <= 9007199254740991 {
			return int64(n)
		}
	}
	panic(vm.NewTypeError(fmt.Sprintf("The %q argument must be a safe integer. Received %s", name, value.String())))
}

func (m *Module) randomUUID(vm *goja.Runtime, call goja.FunctionCall) goja.Value {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(vm.NewGoError(err))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return vm.ToValue(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

func (m *Module) getRandomValues(vm *goja.Runtime, call goja.FunctionCall) goja.Value {
	value := callArg(call, 0)
	meta, ok := typedArrayMeta(vm, value)
	if !ok || meta.isView {
		panic(vm.NewTypeError("The \"typedArray\" argument must be an instance of an integer-typed TypedArray"))
	}
	switch meta.kind {
	case reflect.Int8, reflect.Uint8, reflect.Int16, reflect.Uint16, reflect.Int32, reflect.Uint32, reflect.Int64, reflect.Uint64:
	default:
		panic(vm.NewTypeError("The \"typedArray\" argument must be an instance of an integer-typed TypedArray"))
	}
	if len(meta.raw) > 65536 {
		object := vm.NewGoError(errors.New("getRandomValues() failed, entropy string limit reached"))
		_ = object.Set("name", "QuotaExceededError")
		_ = object.Set("code", 22)
		panic(object)
	}
	if _, err := rand.Read(meta.raw); err != nil {
		panic(vm.NewGoError(err))
	}
	return value
}

func (m *Module) timingSafeEqual(vm *goja.Runtime, call goja.FunctionCall) goja.Value {
	a := callArg(call, 0)
	b := callArg(call, 1)
	if !isArrayBufferView(a) || !isArrayBufferView(b) {
		invalidArgType(vm, "buf", "of type Buffer, TypedArray, or DataView")
	}
	aBytes := viewBytes(vm, a)
	bBytes := viewBytes(vm, b)
	if len(aBytes) != len(bBytes) {
		cryptoThrow(vm, "ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH", "Input buffers must have the same byte length")
	}
	return vm.ToValue(subtle.ConstantTimeCompare(aBytes, bBytes) == 1)
}

// ---------------------------------------------------------------------------
// Key derivation

func (m *Module) pbkdf2(vm *goja.Runtime, call goja.FunctionCall, sync bool) goja.Value {
	password := m.decodeData(vm, callArg(call, 0), "")
	salt := m.decodeData(vm, callArg(call, 1), "")
	iterations := int(callArg(call, 2).ToInteger())
	keylen := int(callArg(call, 3).ToInteger())
	digestName := stringArg(callArg(call, 4))
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
		callback, ok := goja.AssertFunction(callArg(call, 5))
		if !ok {
			invalidArgType(vm, "callback", "a function")
		}
		m.runAsync("pbkdf2", run, func(vm *goja.Runtime, result any) []goja.Value {
			return []goja.Value{buffer.WrapBytes(vm, result.([]byte))}
		}, callback)
		return goja.Undefined()
	}
	result, err := run()
	if err != nil {
		panic(vm.NewGoError(err))
	}
	return buffer.WrapBytes(vm, result.([]byte))
}

type scryptOptions struct {
	n, r, p int
	maxmem  int
}

func (m *Module) scrypt(vm *goja.Runtime, call goja.FunctionCall, sync bool) goja.Value {
	password := m.decodeData(vm, callArg(call, 0), "")
	salt := m.decodeData(vm, callArg(call, 1), "")
	keylen := int(callArg(call, 2).ToInteger())
	if keylen < 0 || keylen > 0x7fffffff {
		outOfRange(vm, "keylen", fmt.Sprintf("It must be >= 0 && <= 2147483647. Received %d", keylen))
	}
	options := scryptOptions{n: 16384, r: 8, p: 1, maxmem: 32 * 1024 * 1024}
	if object, ok := callArg(call, 3).(*goja.Object); ok {
		options.n = scryptIntOption(object, "N", "cost", options.n)
		options.r = scryptIntOption(object, "r", "blockSize", options.r)
		options.p = scryptIntOption(object, "p", "parallelization", options.p)
		options.maxmem = scryptIntOption(object, "maxmem", "", options.maxmem)
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
		callback, ok := goja.AssertFunction(callArg(call, 4))
		if !ok {
			callback, ok = goja.AssertFunction(callArg(call, 3))
		}
		if !ok {
			invalidArgType(vm, "callback", "a function")
		}
		m.runAsync("scrypt", run, func(vm *goja.Runtime, result any) []goja.Value {
			return []goja.Value{buffer.WrapBytes(vm, result.([]byte))}
		}, callback)
		return goja.Undefined()
	}
	result, err := run()
	if err != nil {
		panic(cryptoErrorObject(vm, err))
	}
	return buffer.WrapBytes(vm, result.([]byte))
}

func scryptIntOption(object *goja.Object, name, alias string, fallback int) int {
	if value := object.Get(name); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		return int(value.ToInteger())
	}
	if alias != "" {
		if value := object.Get(alias); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
			return int(value.ToInteger())
		}
	}
	return fallback
}

func (m *Module) hashOneShot(vm *goja.Runtime, call goja.FunctionCall) goja.Value {
	factory, ok := hashFactoryFor(stringArg(callArg(call, 0)))
	if !ok {
		panic(vm.NewTypeError("Digest method not supported"))
	}
	data := m.decodeData(vm, callArg(call, 1), "")
	digest := factory()
	_, _ = digest.Write(data)
	return encodeOutput(vm, digest.Sum(nil), stringArg(callArg(call, 2)))
}

// ---------------------------------------------------------------------------
// Async callbacks

func (m *Module) runAsync(name string, run func() (any, error), values func(*goja.Runtime, any) []goja.Value, callback goja.Callable) {
	go func() {
		result, err := run()
		if !m.runtime.RunOnLoop(func(vm *goja.Runtime) {
			_ = m.runtime.RunJob(vm, "crypto."+name, func() error {
				if err != nil {
					_, callbackErr := callback(goja.Undefined(), cryptoErrorObject(vm, err))
					return callbackErr
				}
				callbackArgs := []goja.Value{goja.Null()}
				callbackArgs = append(callbackArgs, values(vm, result)...)
				_, callbackErr := callback(goja.Undefined(), callbackArgs...)
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
	vm          *goja.Runtime
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

func (m *Module) makeCipherConstructor(vm *goja.Runtime, encrypt bool) goja.Value {
	return vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
		state := m.newCipherState(vm, encrypt, call.Arguments)
		m.attachCipherMethods(vm, call.This, state)
		return nil
	})
}

func (m *Module) newCipherState(vm *goja.Runtime, encrypt bool, arguments []goja.Value) *cipherState {
	spec, ok := parseCipher(stringArg(argAt(arguments, 0)))
	if !ok {
		cryptoThrow(vm, "ERR_CRYPTO_UNKNOWN_CIPHER", "Unknown cipher")
	}
	key := m.decodeData(vm, argAt(arguments, 1), "")
	if len(key) != spec.keyLen {
		cryptoThrow(vm, "ERR_CRYPTO_INVALID_KEYLEN", "Invalid key length")
	}
	ivValue := argAt(arguments, 2)
	var iv []byte
	if !goja.IsUndefined(ivValue) && !goja.IsNull(ivValue) {
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
	if options, ok := argAt(arguments, 3).(*goja.Object); ok {
		if value := options.Get("authTagLength"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
			tagLen = int(value.ToInteger())
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
		panic(vm.NewGoError(err))
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
				panic(vm.NewGoError(gcmErr))
			}
			state.aead = aead
		} else {
			if tagLen != 16 {
				cryptoThrow(vm, "ERR_CRYPTO_INVALID_AUTH_TAG", "Invalid authentication tag length: authTagLength with a non-12-byte IV is not supported")
			}
			aead, gcmErr := cipher.NewGCMWithNonceSize(block, len(iv))
			if gcmErr != nil {
				panic(vm.NewGoError(gcmErr))
			}
			state.aead = aead
		}
	case cipherChaCha:
		if tagLen != 16 {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_AUTH_TAG", fmt.Sprintf("Invalid authentication tag length: %d", tagLen))
		}
		aead, chachaErr := chacha20poly1305.New(key)
		if chachaErr != nil {
			panic(vm.NewGoError(chachaErr))
		}
		state.aead = aead
	}
	return state
}

func (m *Module) attachCipherMethods(vm *goja.Runtime, object *goja.Object, state *cipherState) {
	_ = object.Set("update", func(call goja.FunctionCall) goja.Value {
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation update")
		}
		data := m.decodeData(vm, callArg(call, 0), stringArg(callArg(call, 1)))
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
		return encodeOutput(vm, out, stringArg(callArg(call, 2)))
	})
	_ = object.Set("final", func(call goja.FunctionCall) goja.Value {
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
					panic(vm.NewGoError(errors.New("Unsupported state or unable to authenticate data")))
				}
				plain, err := state.aead.Open(nil, state.nonce, append(state.buffered, state.authTag...), state.aad)
				if err != nil {
					panic(vm.NewGoError(errors.New("Unsupported state or unable to authenticate data")))
				}
				out = plain
			}
		}
		return encodeOutput(vm, out, stringArg(callArg(call, 0)))
	})
	_ = object.Set("setAutoPadding", func(call goja.FunctionCall) goja.Value {
		if state.spec.kind != cipherCBC && state.spec.kind != cipherECB {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAutoPadding")
		}
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAutoPadding")
		}
		autoPadding := true
		if value := callArg(call, 0); !goja.IsUndefined(value) {
			autoPadding = value.ToBoolean()
		}
		state.autoPadding = autoPadding
		return call.This
	})
	_ = object.Set("setAAD", func(call goja.FunctionCall) goja.Value {
		if state.spec.kind != cipherGCM && state.spec.kind != cipherChaCha {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAAD")
		}
		if state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAAD")
		}
		value := callArg(call, 0)
		if !isArrayBufferView(value) {
			invalidArgType(vm, "aad", "an instance of Buffer, TypedArray, or DataView")
		}
		state.aad = append(state.aad, viewBytes(vm, value)...)
		return call.This
	})
	_ = object.Set("setAuthTag", func(call goja.FunctionCall) goja.Value {
		if state.spec.kind != cipherGCM && state.spec.kind != cipherChaCha {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAuthTag")
		}
		if state.encrypt || state.finalized {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation setAuthTag")
		}
		value := callArg(call, 0)
		if !isArrayBufferView(value) {
			invalidArgType(vm, "tag", "an instance of Buffer, TypedArray, or DataView")
		}
		tag := viewBytes(vm, value)
		if len(tag) != state.tagLen {
			cryptoThrow(vm, "ERR_CRYPTO_INVALID_AUTH_TAG", fmt.Sprintf("Invalid authentication tag length: %d", len(tag)))
		}
		state.authTag = append([]byte(nil), tag...)
		state.authTagSet = true
		return call.This
	})
	if state.encrypt && (state.spec.kind == cipherGCM || state.spec.kind == cipherChaCha) {
		_ = object.Set("getAuthTag", func(call goja.FunctionCall) goja.Value {
			if !state.finalized {
				cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation getAuthTag")
			}
			return buffer.WrapBytes(vm, state.authTag)
		})
	}
}

func (m *Module) createCipheriv(vm *goja.Runtime, call goja.FunctionCall, ctor goja.Value) goja.Value {
	object, err := vm.New(ctor, call.Arguments...)
	if err != nil {
		panic(err)
	}
	return object
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
	vm        *goja.Runtime
	algo      signAlgo
	data      []byte
	isSign    bool
	finalized bool
}

func (m *Module) makeSignConstructor(vm *goja.Runtime, isSign bool) goja.Value {
	return vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
		if goja.IsUndefined(call.Argument(0)) || goja.IsNull(call.Argument(0)) {
			invalidArgType(vm, "algorithm", "of type string")
		}
		algo, err := parseSignAlgorithm(stringArg(call.Argument(0)))
		if err != nil {
			panic(cryptoErrorObject(vm, err))
		}
		state := &signState{vm: vm, algo: algo, isSign: isSign}
		_ = call.This.Set("update", func(call goja.FunctionCall) goja.Value {
			if state.finalized {
				cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation update")
			}
			state.data = append(state.data, m.decodeData(vm, callArg(call, 0), stringArg(callArg(call, 1)))...)
			return call.This
		})
		if isSign {
			_ = call.This.Set("sign", func(call goja.FunctionCall) goja.Value {
				if state.finalized {
					cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation sign")
				}
				state.finalized = true
				keyBytes := m.decodeData(vm, callArg(call, 0), "")
				privateKey, parseErr := parsePrivateKey(keyBytes)
				if parseErr != nil {
					panic(cryptoErrorObject(vm, parseErr))
				}
				signature, signErr := computeSignature(state.algo, privateKey, state.data)
				if signErr != nil {
					panic(cryptoErrorObject(vm, signErr))
				}
				return encodeOutput(vm, signature, stringArg(callArg(call, 1)))
			})
		} else {
			_ = call.This.Set("verify", func(call goja.FunctionCall) goja.Value {
				if state.finalized {
					cryptoThrow(vm, "ERR_CRYPTO_INVALID_STATE", "Invalid state for operation verify")
				}
				state.finalized = true
				keyBytes := m.decodeData(vm, callArg(call, 0), "")
				signature := m.decodeData(vm, callArg(call, 1), stringArg(callArg(call, 2)))
				publicKey, parseErr := parsePublicKey(keyBytes)
				if parseErr != nil {
					panic(cryptoErrorObject(vm, parseErr))
				}
				valid, verifyErr := verifySignature(state.algo, publicKey, state.data, signature)
				if verifyErr != nil {
					panic(cryptoErrorObject(vm, verifyErr))
				}
				return vm.ToValue(valid)
			})
		}
		return nil
	})
}

func (m *Module) createSign(vm *goja.Runtime, call goja.FunctionCall, ctor goja.Value) goja.Value {
	object, err := vm.New(ctor, call.Arguments...)
	if err != nil {
		panic(err)
	}
	return object
}

func (m *Module) signOneShot(vm *goja.Runtime, call goja.FunctionCall, isSign bool) goja.Value {
	algorithmValue := callArg(call, 0)
	algorithmName := stringArg(algorithmValue)
	if goja.IsNull(algorithmValue) {
		algorithmName = "ed25519"
	}
	algo, err := parseSignAlgorithm(algorithmName)
	if err != nil {
		panic(cryptoErrorObject(vm, err))
	}
	data := m.decodeData(vm, callArg(call, 1), "")
	keyBytes := m.decodeData(vm, callArg(call, 2), "")
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
		signature := m.decodeData(vm, callArg(call, 3), stringArg(callArg(call, 4)))
		run = func() (any, error) {
			publicKey, parseErr := parsePublicKey(keyBytes)
			if parseErr != nil {
				return nil, parseErr
			}
			return verifySignature(algo, publicKey, data, signature)
		}
	}
	if isSign {
		if callback, ok := goja.AssertFunction(callArg(call, 3)); ok {
			m.runAsync("sign", run, func(vm *goja.Runtime, result any) []goja.Value {
				return []goja.Value{buffer.WrapBytes(vm, result.([]byte))}
			}, callback)
			return goja.Undefined()
		}
	} else if callback, ok := goja.AssertFunction(callArg(call, 4)); ok {
		m.runAsync("verify", run, func(vm *goja.Runtime, result any) []goja.Value {
			return []goja.Value{vm.ToValue(result.(bool))}
		}, callback)
		return goja.Undefined()
	}
	result, err := run()
	if err != nil {
		panic(cryptoErrorObject(vm, err))
	}
	if isSign {
		return buffer.WrapBytes(vm, result.([]byte))
	}
	return vm.ToValue(result.(bool))
}
