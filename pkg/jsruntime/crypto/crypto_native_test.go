package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strconv"
	"testing"
	"time"

	moe "github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/buffer"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/loop"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
)

func runNativeCryptoRegression(t *testing.T, script string) {
	t.Helper()
	results := make(chan string, 8)
	l := loop.NewEventLoop(loop.WithOnError(func(err error) { results <- err.Error() }))
	t.Cleanup(l.Terminate)
	services := bridge.New(l, 10*time.Second)
	services.SetErrorReporter(func(_ *moe.Runtime, _ string, err error) { results <- err.Error() })
	if err := l.Start(); err != nil {
		t.Fatal(err)
	}
	l.RunOnLoop(func(vm *moe.Runtime) {
		buffer.Enable(vm)
		module := vm.Realm().NewObject()
		New(services).Load(vm, module)
		_ = vm.SetGlobal("crypto", native.Get(vm, module, "exports"))
		_ = vm.SetGlobal("done", native.Bind(vm, "done", 1, func(_ *moe.Realm, _ moe.Value, args []moe.Value) (moe.Value, error) {
			results <- native.String(vm, moe.Arg(args, 0))
			return moe.Undefined(), nil
		}))
		if _, err := native.RunScript(vm, "crypto-native-test.js", script); err != nil {
			results <- err.Error()
		}
	})
	select {
	case result := <-results:
		if result != "ok" {
			t.Fatal(result)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("native crypto callback was not delivered")
	}
}

func TestNativeCryptoRandomIntSyncAndAsync(t *testing.T) {
	runNativeCryptoRegression(t, `
 const a = crypto.randomInt(1);
 const b = crypto.randomInt(-3, -2);
 if (typeof a !== "number" || a !== 0 || b !== -3) throw new Error("randomInt must return synchronously without callback");
 let rejected = false;
 try { crypto.randomFill(new Uint8Array(1)); } catch (e) { rejected = e instanceof TypeError; }
 if (!rejected) throw new Error("missing randomFill callback accepted");
 crypto.randomInt(4, 5, (err, value) => done(!err && value === 4 ? "ok" : "randomInt callback"));
 `)
}

func TestNativeCryptoRandomFillLoopOwnership(t *testing.T) {
	runNativeCryptoRegression(t, `
 const backing = new ArrayBuffer(128);
 const view = new Uint16Array(backing, 8, 32);
 const raw = new Uint8Array(backing);
 crypto.randomFill(view, 2, 16, (err, result) => {
  if (err || result !== view) return done("randomFill callback identity");
  if (raw.slice(0, 12).some(x => x !== 123) || raw.slice(44).some(x => x !== 123)) return done("randomFill wrote outside requested view range");
  if (raw.slice(12, 44).every(x => x === 123)) return done("randomFill did not copy on loop");
  done("ok");
 });
 // No loop callback can run yet. This must not overlap a worker writing raw.
 for (let i = 0; i < 1000; i++) raw.fill(123);
 `)
}

func TestNativeCryptoRandomFillDetachAndResize(t *testing.T) {
	for _, mutation := range []string{"ab.transfer()", "ab.resize(4)"} {
		t.Run(mutation, func(t *testing.T) {
			runNativeCryptoRegression(t, `
 const ab = new ArrayBuffer(64, {maxByteLength: 128});
 const view = new Uint8Array(ab, 8, 32);
 let calls = 0;
 crypto.randomFill(view, 2, 16, (err, value) => {
  if (++calls !== 1 || !(err instanceof Error) || value !== undefined) return done("detach/resize must callback with error");
  done("ok");
 });
 `+mutation+`;`)
		})
	}
}

func TestNativeCryptoRandomFillReacquiresResizedStorage(t *testing.T) {
	runNativeCryptoRegression(t, `
 const ab = new ArrayBuffer(64, {maxByteLength: 1024});
 const view = new DataView(ab, 4, 32);
 crypto.randomFill(view, 3, 24, (err, value) => {
  const raw = new Uint8Array(ab);
  if (err || value !== view || raw.slice(7, 31).every(x => x === 0)) return done("stale randomFill storage after resize");
  if (raw.slice(0, 7).some(x => x !== 0) || raw.slice(31).some(x => x !== 0)) return done("randomFill resize range");
  done("ok");
 });
 ab.resize(1024);
 `)
}

func TestNativeCryptoAsyncInputSnapshots(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	runNativeCryptoRegression(t, "const privatePEM = "+strconv.Quote(privatePEM)+"; const publicPEM = "+strconv.Quote(publicPEM)+";"+`
 const password = Buffer.from("password"), salt = Buffer.from("salt");
 const expectedPBKDF = crypto.pbkdf2Sync(password, salt, 1000, 32, "sha256").toString("hex");
 const expectedScrypt = crypto.scryptSync(password, salt, 32, {N: 16, r: 1, p: 1}).toString("hex");
 const data = Buffer.from("message"), privateKey = Buffer.from(privatePEM), publicKey = Buffer.from(publicPEM);
 const signature = crypto.sign(null, data, privateKey);
 let remaining = 4;
 function checked(ok) { if (!ok) throw new Error("async crypto input was not snapshotted"); if (--remaining === 0) done("ok"); }
 crypto.pbkdf2(password, salt, 1000, 32, "sha256", (err, value) => checked(!err && value.toString("hex") === expectedPBKDF));
 crypto.scrypt(password, salt, 32, {N: 16, r: 1, p: 1}, (err, value) => checked(!err && value.toString("hex") === expectedScrypt));
 crypto.sign(null, data, privateKey, (err, value) => checked(!err && value.toString("hex") === expectedSignature));
 const expectedSignature = signature.toString("hex");
 crypto.verify(null, data, publicKey, signature, (err, value) => checked(!err && value === true));
 password.fill(0); salt.fill(0); data.fill(0); privateKey.fill(0); publicKey.fill(0); signature.fill(0);
 `)
}
