// Package plugin manages Komari plugins: ZIP packages with a
// komari-plugin.json manifest, mirroring the theme package format. A plugin
// runs in its own jsruntime instance confined to its data/plugin/<short>
// directory, plus its long-term data directory data/plugin-data/<short>
// exposed as __storageDir__ (which survives plugin updates), declares runtime
// permissions in its manifest, and receives the host-injected "server" module
// (server.route / server.call / server.hook, whose ws kinds can intercept
// WebSocket connections and frames). Plugins run with admin authority inside
// the system.
//
// Lifecycle: plugins are installed as directories under DataDir. The
// persisted state file (state.json) records which plugins are enabled, which
// permissions were approved, and the last load error. Enabled plugins are
// loaded at startup; a failed load auto-disables the plugin and keeps the
// error text visible through admin:listPlugins.
package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/scheduler"
	"github.com/komari-monitor/komari/pkg/jsruntime"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/komari-monitor/komari/web/connection"
)

// DataDir is the on-disk root for installed plugins, mirroring the theme
// directory layout. It is a variable so tests can redirect it.
var DataDir = "./data/plugin"

// StorageDir is the on-disk root for long-term plugin data. Each plugin gets
// StorageDir/<short>. The directory is confined into the plugin runtime as
// __storageDir__ and survives plugin reinstalls: update removes only
// DataDir/<short> (the code from the ZIP) while StorageDir/<short> persists.
// Deleting a plugin removes both.
var StorageDir = "./data/plugin-data"

const (
	manifestFile = "komari-plugin.json"
	defaultEntry = "script.js"

	defaultMaxHTTPBodyBytes int64 = 32 << 20
	defaultLogBufferSize          = 64 << 10

	stateFileName = "state.json"
)

// maxHookBufferBytes caps how much of a response the hook chain buffers
// for rewriting. Larger responses pass through to the client unmodified;
// it is a variable so tests can lower it.
var maxHookBufferBytes = 32 << 20

// maxHTMLInjectBufferBytes caps how much of a text/html response the HTML
// injector buffers before injecting plugin fragments. Larger responses pass
// through unmodified; it is a variable so tests can lower it.
var maxHTMLInjectBufferBytes = 32 << 20

const (
	maxPluginArchiveFiles  = 10000
	maxPluginFileSize      = 128 << 20
	maxPluginExtractedSize = 512 << 20
	maxPluginManifestSize  = 1 << 20
)

// ErrPermissionApprovalRequired is returned when enabling a plugin whose
// declared permissions have not been approved, or changed since approval.
var ErrPermissionApprovalRequired = errors.New("plugin permissions require approval")

var errNotLoaded = errors.New("plugin is not loaded")

// ErrNotInstalled is returned when deleting a plugin that is not installed.
var ErrNotInstalled = errors.New("plugin is not installed")

// Info describes one installed plugin for the admin surface.
type Info struct {
	models.Plugin
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	LastError string `json:"last_error"`
}

// Manager owns the installed plugin set: load/unload lifecycle, persisted
// state, per-plugin logs, and the host engine used by server.route.
type Manager struct {
	mu        sync.RWMutex
	engine    *gin.Engine
	instances map[string]*Instance
	routes    map[string]map[string]bool // short -> "METHOD path" -> gin slot registered
	hooks     []*hookEntry               // http request/response hooks in registration order
	injects   []*injectEntry             // html head/body fragments in registration order

	stateMu sync.Mutex
	state   *State

	logsMu sync.Mutex
	logs   map[string]*LogBuffer
}

// Instance is one loaded plugin runtime plus its host bindings.
type Instance struct {
	mu         sync.RWMutex
	info       models.Plugin
	dir        string
	runtime    *jsruntime.Runtime
	host       *jsruntime.Host
	handlers   map[string]goja.Callable // "METHOD path" -> current route handler
	statics    map[string]*staticConfig // mount path -> static folder config
	rpcMethods map[string]goja.Callable // registered RPC method -> JS handler
	cronJobs   []string                 // scheduler job names, removed on unload
}

// global is the process-wide plugin manager.
var global = &Manager{
	instances: make(map[string]*Instance),
	routes:    make(map[string]map[string]bool),
	logs:      make(map[string]*LogBuffer),
}

// Init wires the manager to the HTTP engine and into every WebSocket
// connection. It must be called before any plugin is loaded.
func Init(engine *gin.Engine) {
	global.mu.Lock()
	global.engine = engine
	global.mu.Unlock()
	connection.SetFrameInterceptor(global)
}

// LoadAll loads every installed plugin whose persisted state is enabled and
// approved. A failed load auto-disables the plugin, persists last_error and
// is returned joined so callers can log it without stopping startup.
func LoadAll() error {
	return global.loadAll()
}

// SetEnabled is the single switch entry point. Enabling a plugin whose
// declared permissions differ from the approved hash requires approved=true.
func SetEnabled(short string, enabled, approved bool) error {
	return global.setEnabled(short, enabled, approved)
}

// Reload unloads and loads an enabled plugin. The load hook completes before
// this function returns; a failed load is persisted as the plugin's last
// error and disables the plugin, matching the normal enable/startup path.
func Reload(short string) error {
	return global.reload(short)
}

// CloseAll unloads every loaded plugin. Used during server shutdown.
func CloseAll() error {
	return global.closeAll()
}

// List returns the installed plugins with their enabled/running state.
func List() []Info {
	return global.list()
}

// GetLogs returns the bounded log buffer of one plugin.
func GetLogs(short string) string {
	return global.getLogs(short)
}

// Delete removes an installed plugin and its persisted state.
func Delete(short string) error {
	return global.delete(short)
}

// WrapHandler installs the plugin HTTP hook chain around the application
// handler. Request hooks run before the handler and may modify the request;
// response hooks run after the handler and may modify status/headers/body.
func WrapHandler(next http.Handler) http.Handler {
	return global.wrapHandler(next)
}

// HTMLInjectHandler installs plugin HTML injection around the application
// handler. Every text/html response is buffered and gets the fragments
// registered through server.injectHTML inserted before </head> and </body>;
// all other responses (JSON, assets, streams, downloads...) pass through to
// the client unmodified.
func HTMLInjectHandler(next http.Handler) http.Handler {
	return global.htmlInjectHandler(next)
}

func (m *Manager) stateStore() *State {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if m.state == nil || m.state.path != filepath.Join(DataDir, stateFileName) {
		m.state = openState(filepath.Join(DataDir, stateFileName))
	}
	return m.state
}

func (m *Manager) logStore(short string) *LogBuffer {
	m.logsMu.Lock()
	defer m.logsMu.Unlock()
	if m.logs == nil {
		m.logs = make(map[string]*LogBuffer)
	}
	buf := m.logs[short]
	if buf == nil {
		buf = newLogBuffer(defaultLogBufferSize)
		m.logs[short] = buf
	}
	return buf
}

func (m *Manager) getLogs(short string) string {
	m.logsMu.Lock()
	defer m.logsMu.Unlock()
	if m.logs == nil {
		return ""
	}
	buf := m.logs[short]
	if buf == nil {
		return ""
	}
	return buf.String()
}

func (m *Manager) instanceFor(short string) *Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.instances[short]
}

// load runs a plugin inside its own jsruntime instance. The manager lock is
// only held while the instance map is mutated; script evaluation and the
// load() hook run without it so the plugin can call server.call (which takes
// a manager read lock through rpcHandler) without deadlocking. On failure the
// instance is dropped and its hooks and RPC methods are cleaned up.
func (m *Manager) load(short string) error {
	dir := filepath.Join(DataDir, short)
	info, err := readManifest(dir)
	if err != nil {
		return err
	}
	if err := CheckKomariVersion(info.Komari); err != nil {
		return err
	}
	script, err := os.ReadFile(filepath.Join(dir, info.Entry))
	if err != nil {
		return fmt.Errorf("read plugin entry %s: %w", info.Entry, err)
	}

	logs := m.logStore(short)
	logs.Reset()
	_, _ = logs.Write([]byte("[plugin] loading " + short + "\n"))

	inst := &Instance{info: info, dir: dir, handlers: make(map[string]goja.Callable), statics: make(map[string]*staticConfig)}
	m.mu.Lock()
	if _, ok := m.instances[short]; ok {
		m.mu.Unlock()
		return fmt.Errorf("plugin %q is already loaded", short)
	}
	m.instances[short] = inst
	m.mu.Unlock()

	storageDir := filepath.Join(StorageDir, short)
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		m.dropInstance(short, inst)
		return fmt.Errorf("create plugin storage dir: %w", err)
	}

	opts := jsruntime.Options{
		BaseDir:             dir,
		StorageDir:          storageDir,
		NodeJS:              info.Permissions.Node,
		AllowExec:           info.Permissions.AllowExec,
		AllowListen:         info.Permissions.AllowListen,
		AllowAllFileAccess:  info.Permissions.AllowAllFileAccess,
		MaxHTTPBodyBytes:    info.Permissions.MaxHTTPBodyBytes,
		MaxChildOutputBytes: info.Permissions.MaxChildOutputBytes,
		Timeout:             time.Duration(info.Permissions.TimeoutSeconds) * time.Second,
		Console:             logs,
		ConfigureHost: func(host *jsruntime.Host, registry *require.Registry) {
			inst.mu.Lock()
			inst.host = host
			inst.mu.Unlock()
			m.registerServerModule(host, registry, inst)
		},
	}
	rt, err := jsruntime.New(string(script), opts)
	if err != nil {
		m.dropInstance(short, inst)
		return fmt.Errorf("initialize plugin %q: %w", short, err)
	}
	inst.mu.Lock()
	inst.runtime = rt
	inst.mu.Unlock()

	if rt.HasFunction("load") {
		if err := rt.CallVoid("load"); err != nil {
			rt.Close()
			m.dropInstance(short, inst)
			return fmt.Errorf("plugin %q load() failed: %w", short, err)
		}
	}
	m.mu.Lock()
	stillLoaded := m.instances[short] == inst
	m.mu.Unlock()
	if !stillLoaded {
		// A concurrent unload claimed this instance while it was loading.
		rt.Close()
		return fmt.Errorf("plugin %q was unloaded while loading", short)
	}
	_, _ = logs.Write([]byte("[plugin] loaded " + short + "\n"))
	return nil
}

// dropInstance removes a failed load from the manager: it detaches the
// instance, unregisters its RPC methods, and clears its route slots and
// hooks. Gin route slots from the failed load stay inert, mirroring the
// unload behavior. The caller must not hold any Manager lock.
func (m *Manager) dropInstance(short string, inst *Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.instances[short] != inst {
		return // replaced by a newer load or already unloaded
	}
	delete(m.instances, short)
	inst.mu.Lock()
	for method := range inst.rpcMethods {
		rpc.Unregister(method)
	}
	removeCronJobs(inst.cronJobs)
	inst.runtime = nil
	inst.host = nil
	clear(inst.handlers)
	clear(inst.statics)
	clear(inst.rpcMethods)
	inst.cronJobs = nil
	inst.mu.Unlock()
	m.removeHooksLocked(short)
	m.removeInjectsLocked(short)
}

// removeCronJobs cancels and forgets the scheduler jobs of one plugin load.
// It runs with the instance lock held; scheduler.Remove only takes the
// scheduler's own lock, so no lock cycle is possible.
func removeCronJobs(names []string) {
	for _, name := range names {
		scheduler.Remove(name)
	}
}

// unload stops one plugin. The instance is claimed (removed from the map)
// under the manager lock, then the unload() hook runs without any lock so
// the script can still call other registered RPC methods. After the hook the
// remaining state is detached under the lock and the runtime is closed.
// Calls to server.call made from unload() itself are rejected with a clean
// "not loaded" error instead of deadlocking.
func (m *Manager) unload(short string) error {
	m.mu.Lock()
	inst, ok := m.instances[short]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %q", errNotLoaded, short)
	}
	delete(m.instances, short)
	m.mu.Unlock()

	inst.mu.RLock()
	rt := inst.runtime
	inst.mu.RUnlock()
	var unloadErr error
	if rt != nil {
		if rt.HasFunction("unload") {
			if err := rt.CallVoid("unload"); err != nil {
				unloadErr = fmt.Errorf("plugin %q unload() failed: %w", short, err)
			}
		}
	}
	m.mu.Lock()
	inst.mu.Lock()
	for method := range inst.rpcMethods {
		rpc.Unregister(method)
	}
	removeCronJobs(inst.cronJobs)
	inst.runtime = nil
	inst.host = nil
	clear(inst.handlers)
	clear(inst.statics)
	clear(inst.rpcMethods)
	inst.cronJobs = nil
	inst.mu.Unlock()
	m.removeHooksLocked(short)
	m.removeInjectsLocked(short)
	m.mu.Unlock()
	if rt != nil {
		rt.Close()
	}
	_, _ = m.logStore(short).Write([]byte("[plugin] unloaded " + short + "\n"))
	return unloadErr
}

func (m *Manager) closeAll() error {
	m.mu.RLock()
	shorts := make([]string, 0, len(m.instances))
	for short := range m.instances {
		shorts = append(shorts, short)
	}
	m.mu.RUnlock()
	var errs []error
	for _, short := range shorts {
		if err := m.unload(short); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) setEnabled(short string, enabled, approved bool) error {
	dir := filepath.Join(DataDir, short)
	info, err := readManifest(dir)
	if err != nil {
		return err
	}
	if !enabled {
		st := m.stateStore().get(short)
		st.Enabled = false
		if err := m.unload(short); err != nil && !errors.Is(err, errNotLoaded) {
			st.LastError = err.Error()
		}
		m.stateStore().set(short, st)
		return nil
	}
	if m.instanceFor(short) != nil {
		// Already running: enabling is idempotent. Re-running load() would
		// fail with "already loaded" and auto-disable the plugin.
		st := m.stateStore().get(short)
		st.Enabled = true
		st.LastError = ""
		m.stateStore().set(short, st)
		return nil
	}

	hash := approvalPermissionsHash(info.Permissions)
	st := m.stateStore().get(short)
	if permissionsRequireApproval(info.Permissions) && st.ApprovedPermissionsHash != hash {
		if !approved {
			return ErrPermissionApprovalRequired
		}
		st.ApprovedPermissionsHash = hash
		m.stateStore().set(short, st)
	}
	if err := m.load(short); err != nil {
		st.Enabled = false
		st.LastError = err.Error()
		m.stateStore().set(short, st)
		return err
	}
	st.Enabled = true
	st.LastError = ""
	m.stateStore().set(short, st)
	return nil
}

func (m *Manager) loadAll() error {
	entries, err := os.ReadDir(DataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		short := entry.Name()
		st := m.stateStore().get(short)
		if !st.Enabled || m.instanceFor(short) != nil {
			continue
		}
		info, err := readManifest(filepath.Join(DataDir, short))
		if err != nil {
			errs = append(errs, m.disableWithError(short, st, err))
			continue
		}
		if permissionsRequireApproval(info.Permissions) &&
			st.ApprovedPermissionsHash != approvalPermissionsHash(info.Permissions) {
			errs = append(errs, m.disableWithError(short, st, ErrPermissionApprovalRequired))
			continue
		}
		if err := m.load(short); err != nil {
			errs = append(errs, m.disableWithError(short, st, err))
			continue
		}
	}
	return errors.Join(errs...)
}

// restartPlugin brings a plugin back to its persisted enabled state after a
// reinstall. A failed reload or a changed approval hash auto-disables the
// plugin and keeps the error visible, mirroring the startup load path.
func (m *Manager) restartPlugin(short string) error {
	st := m.stateStore().get(short)
	info, err := readManifest(filepath.Join(DataDir, short))
	if err != nil {
		return m.disableWithError(short, st, err)
	}
	if permissionsRequireApproval(info.Permissions) &&
		st.ApprovedPermissionsHash != approvalPermissionsHash(info.Permissions) {
		return m.disableWithError(short, st, ErrPermissionApprovalRequired)
	}
	if err := m.load(short); err != nil {
		return m.disableWithError(short, st, err)
	}
	st.Enabled = true
	st.LastError = ""
	m.stateStore().set(short, st)
	return nil
}

func (m *Manager) reload(short string) error {
	if !m.stateStore().get(short).Enabled {
		return nil
	}
	if err := m.unload(short); err != nil && !errors.Is(err, errNotLoaded) {
		return err
	}
	return m.restartPlugin(short)
}

// disableWithError persists the auto-disable state for one plugin and wraps
// the load failure for the startup error summary.
func (m *Manager) disableWithError(short string, st PluginState, err error) error {
	st.Enabled = false
	st.LastError = err.Error()
	m.stateStore().set(short, st)
	return fmt.Errorf("plugin %q: %w", short, err)
}

func (m *Manager) list() []Info {
	entries, err := os.ReadDir(DataDir)
	if err != nil {
		return []Info{}
	}
	infos := make([]Info, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		short := entry.Name()
		info, err := readManifest(filepath.Join(DataDir, short))
		if err != nil {
			continue // mirror the theme list: skip unreadable entries
		}
		st := m.stateStore().get(short)
		infos = append(infos, Info{
			Plugin:    info,
			Enabled:   st.Enabled,
			Running:   m.instanceFor(short) != nil,
			LastError: st.LastError,
		})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Short < infos[j].Short })
	return infos
}

// Delete removes an installed plugin: it unloads the runtime, deletes the
// plugin directory and clears its persisted state. Registered gin route
// slots stay inert (404) until the server restarts.
func (m *Manager) delete(short string) error {
	if err := m.unload(short); err != nil && !errors.Is(err, errNotLoaded) {
		return err
	}
	if !validShort(short) {
		return fmt.Errorf("invalid plugin short %q", short)
	}
	dir := filepath.Join(DataDir, short)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %q", ErrNotInstalled, short)
		}
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(StorageDir, short)); err != nil {
		return err
	}
	m.stateStore().delete(short)
	return nil
}

// Manifest returns the parsed manifest of an installed plugin.
func Manifest(short string) (models.Plugin, error) {
	if !validShort(short) {
		return models.Plugin{}, fmt.Errorf("invalid plugin short %q", short)
	}
	return readManifest(filepath.Join(DataDir, short))
}

// ResolvePublicFile returns the absolute path of a file that belongs to a
// public iframe page of an installed plugin. Public pages are served without
// authentication: the plugin must be enabled and the requested file must live
// inside the directory of a page declared with visibility=public, so the page
// HTML and its relative assets are reachable without exposing the whole
// plugin directory.
func ResolvePublicFile(short, name string) (string, error) {
	if !validShort(short) {
		return "", fmt.Errorf("invalid plugin short %q", short)
	}
	if !global.stateStore().get(short).Enabled {
		return "", fmt.Errorf("plugin %q is not enabled", short)
	}
	info, err := readManifest(filepath.Join(DataDir, short))
	if err != nil {
		return "", err
	}
	name = filepath.Clean(strings.TrimPrefix(name, "/"))
	for _, page := range info.Pages {
		if page.Visibility != models.PageVisibilityPublic || page.Type != models.PageTypeIframe {
			continue
		}
		dir := filepath.Dir(page.File)
		if dir == "." || name == dir || strings.HasPrefix(name, dir+string(os.PathSeparator)) {
			return ResolveFile(short, name)
		}
	}
	return "", fmt.Errorf("plugin page %q is not public", name)
}

// ResolveFile returns the absolute path of a file inside an installed plugin
// directory, rejecting traversal. Used to serve injected plugin pages.
func ResolveFile(short, name string) (string, error) {
	if !validShort(short) {
		return "", fmt.Errorf("invalid plugin short %q", short)
	}
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("invalid plugin file path %q", name)
	}
	dir := filepath.Join(DataDir, short)
	full := filepath.Join(dir, name)
	if !withinDir(full, dir) {
		return "", fmt.Errorf("invalid plugin file path %q", name)
	}
	if _, err := os.Stat(full); err != nil {
		return "", err
	}
	return full, nil
}

// hooksOf returns a snapshot of the hooks with the given kind.
func (m *Manager) hooksOf(kind hookKind) []*hookEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snapshot := make([]*hookEntry, 0, len(m.hooks))
	for _, hook := range m.hooks {
		if hook.kind == kind {
			snapshot = append(snapshot, hook)
		}
	}
	return snapshot
}

// registerHook appends a hook. Called from the plugin's own event loop
// during script evaluation; it takes the manager lock itself. matcher is
// nil for unfiltered hooks; the plugin-declared body limit is captured at
// registration so unloaded plugins never contribute a stale limit.
func (m *Manager) registerHook(short string, kind hookKind, fn goja.Callable, matcher *hookMatcher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.instances[short]
	if inst == nil {
		return
	}
	inst.mu.RLock()
	host := inst.host
	bodyLimit := inst.info.Permissions.MaxHTTPBodyBytes
	inst.mu.RUnlock()
	m.hooks = append(m.hooks, &hookEntry{short: short, kind: kind, fn: fn, host: host, matcher: matcher, bodyLimit: bodyLimit})
}

// removeHooksLocked drops all hooks of one plugin. The caller must hold the
// manager write lock.
func (m *Manager) removeHooksLocked(short string) {
	kept := m.hooks[:0]
	for _, hook := range m.hooks {
		if hook.short != short {
			kept = append(kept, hook)
		}
	}
	m.hooks = kept
}

// injectsSnapshot returns the registered HTML injection fragments in
// registration order.
func (m *Manager) injectsSnapshot() []*injectEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]*injectEntry(nil), m.injects...)
}

// registerInject appends one plugin's HTML head/body fragments. Called from
// the plugin's own event loop during script evaluation; it takes the manager
// lock itself.
func (m *Manager) registerInject(short, head, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.instances[short] == nil {
		return
	}
	m.injects = append(m.injects, &injectEntry{short: short, head: head, body: body})
}

// removeInjectsLocked drops all injection fragments of one plugin. The
// caller must hold the manager write lock.
func (m *Manager) removeInjectsLocked(short string) {
	kept := m.injects[:0]
	for _, inject := range m.injects {
		if inject.short != short {
			kept = append(kept, inject)
		}
	}
	m.injects = kept
}

// validShort mirrors the theme short-name rule.
func validShort(short string) bool {
	if short == "" || short == "default" {
		return false
	}
	for _, r := range short {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// permissionsRequireApproval reports whether the manifest declares at least
// one approval-relevant capability. Plugins without dangerous capabilities
// (for example only declaring node modules, limits or the timeout) enable
// without an approval step.
func permissionsRequireApproval(p models.PluginPermissions) bool {
	return p.AllowSystemRPC || p.AllowRoutes || p.AllowHooks || p.AllowHTMLInject ||
		p.AllowExec || p.AllowListen || p.AllowAllFileAccess
}

// approvalPermissionsHash canonicalizes the approval-relevant permissions so
// approval can be compared across plugin upgrades. Runtime settings such as
// node modules, resource limits and the execution timeout are granted by
// default and do not participate in the approval hash.
func approvalPermissionsHash(p models.PluginPermissions) string {
	data, _ := json.Marshal(models.PluginPermissions{
		AllowSystemRPC:     p.AllowSystemRPC,
		AllowRoutes:        p.AllowRoutes,
		AllowHooks:         p.AllowHooks,
		AllowHTMLInject:    p.AllowHTMLInject,
		AllowExec:          p.AllowExec,
		AllowListen:        p.AllowListen,
		AllowAllFileAccess: p.AllowAllFileAccess,
	})
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
