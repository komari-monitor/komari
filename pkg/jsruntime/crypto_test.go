package jsruntime

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"testing"
	"time"
)

func newCryptoTestRuntime(t *testing.T, script string) *Runtime {
	t.Helper()
	baseDir := t.TempDir()
	runtime, err := New(script, Options{NodeJS: true, BaseDir: baseDir, Console: io.Discard, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

func TestCryptoHashAndHMAC(t *testing.T) {
	runtime := newCryptoTestRuntime(t, `
		async function verify() {
			const crypto = require("crypto");
			if (require("crypto") !== require("node:crypto")) return "module identity";
			const hash = crypto.createHash("sha256");
			hash.update("m").update("sg");
			const sha256 = hash.digest("hex");
			const sha256HexInput = crypto.createHash("sha256").update("deadbeef", "hex").digest("hex");
			const sha3 = crypto.createHash("sha3-256").update("msg").digest("hex");
			const md5 = crypto.createHash("md5").update("msg").digest("hex");
			const sha512224 = crypto.createHash("sha512-224").update("msg").digest("hex");
			const ripemd = crypto.createHash("ripemd160").update("msg").digest("hex");
			const blake = crypto.createHash("blake2b512").update("msg").digest("hex");
			if (blake !== crypto.hash("blake2b512", "msg", "hex")) return "oneshot mismatch";
			const hmac = crypto.createHmac("sha256", "key").update("msg").digest("hex");
			const hmacB64u = crypto.createHmac("sha256", "key").update("msg").digest("base64url");
			const copy = crypto.createHash("sha256");
			copy.update("a");
			const clone = copy.copy();
			clone.update("b");
			const hmacBufferKey = crypto.createHmac("sha256", Buffer.from("key")).update("msg").digest("hex");
			const instance = crypto.createHash("sha256") instanceof crypto.Hash;
			const hmacInstance = crypto.createHmac("sha256", "k") instanceof crypto.Hmac;
			const oneshot = crypto.hash("sha256", "msg", "hex");
			let finalizedError = null;
			const used = crypto.createHash("sha256");
			used.update("a");
			used.digest();
			try { used.update("b"); } catch (error) { finalizedError = error.code; }
			let unknownDigest = null;
			try { crypto.createHash("no-such-digest"); } catch (error) { unknownDigest = String(error.message); }
			return sha256 === "e46b320165eec91e6344fa10340d5b3208304d6cad29d0d5aed18466d1d9d80e" &&
				sha256HexInput === "5f78c33274e43fa9de5659265c1d917e25c03722dcb0b8d27db8d5feaa813953" &&
				sha3 === "87da20e8da9c58e355cae2ee140b2109f2b5256860d153052070108de2452e72" &&
				md5 === "6e2baaf3b97dbeef01c0043275f9a0e7" &&
				sha512224 === "96144ffa45fc368f4f8431dabec44d62fbc987ba89b078e30dbe33c3" &&
				ripemd === "1986f4891dc4e91212f452ba64af540b949303da" &&
				hmac === "2d93cbc1be167bcb1637a4a23cbff01a7878f0c50ee833954ea5221bb1b8c628" &&
				hmacB64u === "LZPLwb4We8sWN6SiPL_wGnh48MUO6DOVTqUiG7G4xig" &&
				hmacBufferKey === hmac &&
				copy.digest("hex") === "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb" &&
				clone.digest("hex") === "fb8e20fc2e4c3f248c60c39bd652f3c1347298bb977b8b4d5903b85055620603" &&
				instance && hmacInstance && oneshot === sha256 &&
				finalizedError === "ERR_CRYPTO_HASH_FINALIZED" &&
				unknownDigest === "Digest method not supported" &&
				crypto.getHashes().includes("sha3-256") && crypto.getCiphers().includes("aes-256-gcm") &&
				crypto.constants.RSA_PKCS1_PADDING === 1;
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("crypto hash/hmac verification failed: %v", err)
	}
}

func TestCryptoRandom(t *testing.T) {
	runtime := newCryptoTestRuntime(t, `
		async function verify() {
			const crypto = require("node:crypto");
			const bytes = crypto.randomBytes(16);
			if (!Buffer.isBuffer && bytes.length !== 16) return "randomBytes length";
			const asyncBytes = await new Promise((resolve, reject) =>
				crypto.randomBytes(8, (error, value) => error ? reject(error) : resolve(value)));
			if (asyncBytes.length !== 8) return "randomBytes callback";
			const uuid = crypto.randomUUID();
			if (!/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(uuid)) return "uuid format";
			if (crypto.randomUUID() === uuid) return "uuid uniqueness";
			const view = new Uint32Array(4);
			if (crypto.getRandomValues(view) !== view || view.length !== 4) return "getRandomValues";
			let floatRejected = false;
			try { crypto.getRandomValues(new Float32Array(2)); } catch (error) { floatRejected = error instanceof TypeError; }
			if (!floatRejected) return "getRandomValues float";
			const u16 = new Uint16Array(4);
			crypto.randomFillSync(u16, 1, 2);
			if (u16[0] !== 0 || u16[3] !== 0) return "randomFillSync u16 offsets";
			const dv = new DataView(new ArrayBuffer(6));
			crypto.randomFillSync(dv, 2, 3);
			const dvBytes = new Uint8Array(dv.buffer);
			if (dvBytes[0] !== 0 || dvBytes[1] !== 0 || dvBytes[5] !== 0) return "randomFillSync dataview";
			const filled = await new Promise((resolve, reject) => {
				const target = new Uint8Array(4);
				crypto.randomFill(target, 1, 2, (error, value) => error ? reject(error) : resolve(value === target ? target : null));
			});
			if (filled === null || filled[0] !== 0 || filled[3] !== 0) return "randomFill callback";
			let fillRangeError = null;
			try { crypto.randomFillSync(new Uint8Array(4), 2, 5); } catch (error) { fillRangeError = error.code; }
			if (fillRangeError !== "ERR_OUT_OF_RANGE") return "randomFillSync range";
			const ri = crypto.randomInt(10);
			const ri2 = crypto.randomInt(5, 10);
			if (ri < 0 || ri >= 10 || ri2 < 5 || ri2 >= 10) return "randomInt range";
			const riAsync = await new Promise((resolve, reject) =>
				crypto.randomInt(1, 100, (error, value) => error ? reject(error) : resolve(value)));
			if (riAsync < 1 || riAsync >= 100) return "randomInt callback";
			if (!crypto.timingSafeEqual(Buffer.from("ab"), Buffer.from("ab"))) return "timingSafeEqual equal";
			if (crypto.timingSafeEqual(Buffer.from("ab"), Buffer.from("ac"))) return "timingSafeEqual unequal";
			let timingError = null;
			try { crypto.timingSafeEqual(Buffer.from("ab"), Buffer.from("abc")); } catch (error) { timingError = error.code; }
			return timingError === "ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH";
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("crypto random verification failed: %v", err)
	}
}

func TestCryptoKeyDerivation(t *testing.T) {
	runtime := newCryptoTestRuntime(t, `
		async function verify() {
			const crypto = require("node:crypto");
			const pbkdf2 = crypto.pbkdf2Sync("pw", "salt", 1000, 16, "sha256").toString("hex");
			const pbkdf2HexSalt = crypto.pbkdf2Sync("pw", Buffer.from("73616c74", "hex"), 1000, 16, "sha256").toString("hex");
			const scrypt = crypto.scryptSync("pw", "salt", 16).toString("hex");
			const scryptCost = crypto.scryptSync("pw", "salt", 16, { N: 1024, r: 8, p: 1 }).toString("hex");
			const asyncPbkdf2 = await new Promise((resolve, reject) =>
				crypto.pbkdf2("pw", "salt", 1000, 16, "sha256", (error, key) => error ? reject(error) : resolve(key.toString("hex"))));
			const asyncScrypt = await new Promise((resolve, reject) =>
				crypto.scrypt("pw", "salt", 16, (error, key) => error ? reject(error) : resolve(key.toString("hex"))));
			let iterError = null;
			try { crypto.pbkdf2Sync("p", "s", 0, 16, "sha256"); } catch (error) { iterError = error.code; }
			let scryptParamError = null;
			try { crypto.scryptSync("p", "s", 16, { N: 1000 }); } catch (error) { scryptParamError = error.code; }
			let scryptMemError = null;
			try { crypto.scryptSync("p", "s", 16, { N: 16384, r: 8, maxmem: 1024 }); } catch (error) { scryptMemError = error.code; }
			return pbkdf2 === "0a38253555ce37f5c72a6b703f996814" &&
				pbkdf2HexSalt === pbkdf2 &&
				scrypt === "c0b515908e61334cae6d6003c00be60e" &&
				scryptCost === "9f1f8695838e682c1689750f45a69fb9" &&
				asyncPbkdf2 === pbkdf2 && asyncScrypt === scrypt &&
				iterError === "ERR_OUT_OF_RANGE" &&
				scryptParamError === "ERR_CRYPTO_INVALID_SCRYPT_PARAMS" &&
				scryptMemError === "ERR_CRYPTO_INVALID_SCRYPT_PARAMS";
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("crypto key derivation verification failed: %v", err)
	}
}

func TestCryptoCiphers(t *testing.T) {
	runtime := newCryptoTestRuntime(t, `
		function joinBuffers(parts) {
			let total = 0;
			for (const part of parts) total += part.length;
			const out = Buffer.alloc(total);
			let offset = 0;
			for (const part of parts) {
				for (let i = 0; i < part.length; i++) out[offset + i] = part[i];
				offset += part.length;
			}
			return out;
		}
		async function verify() {
			const crypto = require("node:crypto");
			const key = Buffer.alloc(32, 1);
			const iv = Buffer.alloc(16, 2);
			// AES-256-CBC with padding, multi-block input.
			const cbc = crypto.createCipheriv("aes-256-cbc", key, iv);
			const cbcParts = [cbc.update("komari is a monitoring platform with a lot of data")];
			cbcParts.push(cbc.final());
			const cbcEncrypted = joinBuffers(cbcParts);
			const decbc = crypto.createDecipheriv("aes-256-cbc", key, iv);
			const decbcParts = [decbc.update(cbcEncrypted)];
			decbcParts.push(decbc.final());
			if (joinBuffers(decbcParts).toString() !== "komari is a monitoring platform with a lot of data") return "cbc";
			// CBC with no padding.
			const cbcRaw = Buffer.alloc(32, 7);
			const cbcNopad = crypto.createCipheriv("aes-256-cbc", key, iv);
			cbcNopad.setAutoPadding(false);
			const cbcNopadParts = [cbcNopad.update(cbcRaw), cbcNopad.final()];
			const cbcNopadEncrypted = joinBuffers(cbcNopadParts);
			const decbcNopad = crypto.createDecipheriv("aes-256-cbc", key, iv);
			decbcNopad.setAutoPadding(false);
			const decbcNopadParts = [decbcNopad.update(cbcNopadEncrypted), decbcNopad.final()];
			if (!joinBuffers(decbcNopadParts).equals(cbcRaw)) return "cbc nopad";
			// CBC with string output encoding.
			const cbcHex = crypto.createCipheriv("aes-256-cbc", key, iv);
			const cbcHexText = cbcHex.update("hex out", "utf8", "hex") + cbcHex.final("hex");
			const decbcHex = crypto.createDecipheriv("aes-256-cbc", key, iv);
			const decbcHexText = decbcHex.update(cbcHexText, "hex", "utf8") + decbcHex.final("utf8");
			if (decbcHexText !== "hex out") return "cbc hex";
			// AES-256-CTR.
			const ctr = crypto.createCipheriv("aes-256-ctr", key, iv);
			const ctrParts = [ctr.update("stream cipher data")];
			ctrParts.push(ctr.final());
			const ctrEncrypted = joinBuffers(ctrParts);
			const dctr = crypto.createDecipheriv("aes-256-ctr", key, iv);
			const dctrParts = [dctr.update(ctrEncrypted)];
			dctrParts.push(dctr.final());
			if (joinBuffers(dctrParts).toString() !== "stream cipher data") return "ctr";
			// AES-256-ECB.
			const ecb = crypto.createCipheriv("aes-256-ecb", key, null);
			const ecbParts = [ecb.update("ecb block cipher")];
			ecbParts.push(ecb.final());
			const ecbEncrypted = joinBuffers(ecbParts);
			const decb = crypto.createDecipheriv("aes-256-ecb", key, null);
			const decbParts = [decb.update(ecbEncrypted)];
			decbParts.push(decb.final());
			if (joinBuffers(decbParts).toString() !== "ecb block cipher") return "ecb";
			// AES-256-GCM with AAD and a 12-byte IV.
			const gcmKey = Buffer.alloc(32, 3);
			const gcmIV = Buffer.alloc(12, 4);
			const gcm = crypto.createCipheriv("aes-256-gcm", gcmKey, gcmIV);
			gcm.setAAD(Buffer.from("aad data"));
			const gcmParts = [gcm.update("authenticated secret")];
			gcmParts.push(gcm.final());
			const gcmEncrypted = joinBuffers(gcmParts);
			const gcmTag = gcm.getAuthTag();
			if (gcmTag.length !== 16) return "gcm tag length";
			const dgcm = crypto.createDecipheriv("aes-256-gcm", gcmKey, gcmIV);
			dgcm.setAAD(Buffer.from("aad data"));
			dgcm.setAuthTag(gcmTag);
			const dgcmParts = [dgcm.update(gcmEncrypted)];
			dgcmParts.push(dgcm.final());
			if (joinBuffers(dgcmParts).toString() !== "authenticated secret") return "gcm";
			// GCM tampered tag must fail at final().
			const badTag = Buffer.from(gcmTag);
			badTag[0] ^= 0xff;
			const dgcmBad = crypto.createDecipheriv("aes-256-gcm", gcmKey, gcmIV);
			dgcmBad.setAAD(Buffer.from("aad data"));
			dgcmBad.setAuthTag(badTag);
			dgcmBad.update(gcmEncrypted);
			let tamperFailed = false;
			try { dgcmBad.final(); } catch (error) { tamperFailed = String(error.message).includes("Unsupported state or unable to authenticate data"); }
			if (!tamperFailed) return "gcm tamper";
			// GCM without setAuthTag must fail at final().
			const dgcmNoTag = crypto.createDecipheriv("aes-256-gcm", gcmKey, gcmIV);
			dgcmNoTag.update(gcmEncrypted);
			let noTagFailed = false;
			try { dgcmNoTag.final(); } catch (error) { noTagFailed = true; }
			if (!noTagFailed) return "gcm missing tag";
			// GCM getAuthTag before final throws.
			const gcmEarly = crypto.createCipheriv("aes-256-gcm", gcmKey, gcmIV);
			let earlyTagError = null;
			try { gcmEarly.getAuthTag(); } catch (error) { earlyTagError = error.code; }
			if (earlyTagError !== "ERR_CRYPTO_INVALID_STATE") return "gcm early tag";
			// GCM with an 8-byte IV (non-standard nonce length).
			const gcmIV8 = Buffer.alloc(8, 5);
			const gcm8 = crypto.createCipheriv("aes-256-gcm", gcmKey, gcmIV8);
			const gcm8Parts = [gcm8.update("short iv")];
			gcm8Parts.push(gcm8.final());
			const gcm8Encrypted = joinBuffers(gcm8Parts);
			const dgcm8 = crypto.createDecipheriv("aes-256-gcm", gcmKey, gcmIV8);
			dgcm8.setAuthTag(gcm8.getAuthTag());
			const dgcm8Parts = [dgcm8.update(gcm8Encrypted)];
			dgcm8Parts.push(dgcm8.final());
			if (joinBuffers(dgcm8Parts).toString() !== "short iv") return "gcm iv8";
			// GCM with authTagLength 12.
			const gcm12 = crypto.createCipheriv("aes-256-gcm", gcmKey, gcmIV, { authTagLength: 12 });
			gcm12.update("tag12");
			gcm12.final();
			if (gcm12.getAuthTag().length !== 12) return "gcm tag12";
			// ChaCha20-Poly1305.
			const chachaKey = Buffer.alloc(32, 6);
			const chachaIV = Buffer.alloc(12, 7);
			const chacha = crypto.createCipheriv("chacha20-poly1305", chachaKey, chachaIV);
			const chachaParts = [chacha.update("chacha message")];
			chachaParts.push(chacha.final());
			const chachaEncrypted = joinBuffers(chachaParts);
			const dchacha = crypto.createDecipheriv("chacha20-poly1305", chachaKey, chachaIV);
			dchacha.setAuthTag(chacha.getAuthTag());
			const dchachaParts = [dchacha.update(chachaEncrypted)];
			dchachaParts.push(dchacha.final());
			if (joinBuffers(dchachaParts).toString() !== "chacha message") return "chacha";
			// Construction errors.
			let unknownCipher = null;
			try { crypto.createCipheriv("no-such-cipher", key, iv); } catch (error) { unknownCipher = error.code; }
			let badKeyLen = null;
			try { crypto.createCipheriv("aes-256-cbc", Buffer.alloc(15), iv); } catch (error) { badKeyLen = error.code; }
			let badIV = null;
			try { crypto.createCipheriv("aes-256-cbc", key, Buffer.alloc(8)); } catch (error) { badIV = error.code; }
			let ecbIV = null;
			try { crypto.createCipheriv("aes-256-ecb", key, iv); } catch (error) { ecbIV = error.code; }
			let badTagLength = null;
			try { crypto.createCipheriv("aes-256-gcm", gcmKey, gcmIV, { authTagLength: 8 }); } catch (error) { badTagLength = error.code; }
			return unknownCipher === "ERR_CRYPTO_UNKNOWN_CIPHER" && badKeyLen === "ERR_CRYPTO_INVALID_KEYLEN" &&
				badIV === "ERR_CRYPTO_INVALID_IV" && ecbIV === "ERR_CRYPTO_INVALID_IV" &&
				badTagLength === "ERR_CRYPTO_INVALID_AUTH_TAG";
		}
	`)
	if err := runtime.Call("verify"); err != nil {
		t.Fatalf("crypto cipher verification failed: %v", err)
	}
}

func pemTestKeys(t *testing.T) (rsaPriv, rsaPub, ecPriv, ecPub, edPriv, edPub string) {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaKeyDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	rsaPubDER, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	rsaPriv = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaKeyDER}))
	rsaPub = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: rsaPubDER}))

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecKeyDER, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	ecPubDER, err := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecKeyDER}))
	ecPub = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: ecPubDER}))

	edPubKey, edPrivKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edKeyDER, err := x509.MarshalPKCS8PrivateKey(edPrivKey)
	if err != nil {
		t.Fatal(err)
	}
	edPubDER, err := x509.MarshalPKIXPublicKey(edPubKey)
	if err != nil {
		t.Fatal(err)
	}
	edPriv = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: edKeyDER}))
	edPub = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: edPubDER}))
	return rsaPriv, rsaPub, ecPriv, ecPub, edPriv, edPub
}

func TestCryptoSignVerify(t *testing.T) {
	rsaPriv, rsaPub, ecPriv, ecPub, edPriv, edPub := pemTestKeys(t)
	script := `
		async function verify(rsaPriv, rsaPub, ecPriv, ecPub, edPriv, edPub) {
			const crypto = require("node:crypto");
			const rsaSign = crypto.createSign("RSA-SHA256");
			rsaSign.update("hello");
			const rsaSignature = rsaSign.sign(rsaPriv);
			if (rsaSignature.length !== 256) return "rsa sig length";
			const rsaOK = crypto.createVerify("RSA-SHA256").update("hello").verify(rsaPub, rsaSignature);
			const rsaTampered = crypto.createVerify("RSA-SHA256").update("hello").verify(rsaPub, Buffer.from(rsaSignature));
			rsaSignature[0] ^= 0xff;
			const rsaTamperedOK = crypto.createVerify("RSA-SHA256").update("hello").verify(rsaPub, rsaSignature);
			if (!rsaOK || !rsaTampered || rsaTamperedOK) return "rsa verify";
			const oneShotSig = crypto.sign("sha256", Buffer.from("hello"), rsaPriv);
			if (!crypto.verify("sha256", Buffer.from("hello"), rsaPub, oneShotSig)) return "rsa oneshot";
			const asyncSig = await new Promise((resolve, reject) =>
				crypto.sign("sha256", Buffer.from("hello"), rsaPriv, (error, value) => error ? reject(error) : resolve(value)));
			if (!crypto.verify("sha256", Buffer.from("hello"), rsaPub, asyncSig)) return "rsa async";
			const ecSig = crypto.sign("sha256", Buffer.from("hello"), ecPriv);
			if (!crypto.verify("sha256", Buffer.from("hello"), ecPub, ecSig)) return "ec verify";
			if (crypto.verify("sha256", Buffer.from("hello"), ecPub, Buffer.alloc(70))) return "ec garbage";
			const edSig = crypto.sign(null, Buffer.from("hello"), edPriv);
			if (!crypto.verify(null, Buffer.from("hello"), edPub, edSig)) return "ed25519 verify";
			const edSig2 = crypto.sign("ed25519", Buffer.from("hello"), edPriv);
			if (!crypto.verify("ed25519", Buffer.from("hello"), edPub, edSig2)) return "ed25519 named";
			let wrongKeyError = null;
			try { crypto.sign("sha256", Buffer.from("x"), edPriv); } catch (error) { wrongKeyError = error.code; }
			let nullAlgorithmError = null;
			try { crypto.createSign(null); } catch (error) { nullAlgorithmError = error.name; }
			let badAlgorithmError = null;
			try { crypto.createSign("no-such-algo"); } catch (error) { badAlgorithmError = error.code; }
			let badKeyError = null;
			try { crypto.sign("sha256", Buffer.from("x"), "not a pem"); } catch (error) { badKeyError = error.code; }
			return wrongKeyError === "ERR_OSSL_INVALID_DIGEST" &&
				nullAlgorithmError === "TypeError" &&
				badAlgorithmError === "ERR_CRYPTO_INVALID_DIGEST" &&
				badKeyError === "ERR_OSSL_PEM_NO_START_LINE";
		}
	`
	runtime := newCryptoTestRuntime(t, script)
	if err := runtime.Call("verify", rsaPriv, rsaPub, ecPriv, ecPub, edPriv, edPub); err != nil {
		t.Fatalf("crypto sign/verify failed: %v", err)
	}
}
