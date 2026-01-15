package plugin

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/dop251/goja"
	"github.com/gin-gonic/gin"
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/komari-monitor/komari/internal/jsruntime"
	"github.com/patrickmn/go-cache"
)

var (
	kv            *jsruntime.RamKv
	activePlugins *cache.Cache
	id2Path       *cache.Cache
)

func init() {
	// 初始化插件系统, 新建KV内存缓存，建立ID目录索引
	event.On(eventType.ProcessStart, event.ListenerFunc(func(e event.Event) error {
		if err := os.MkdirAll("./data/plugins", os.ModePerm); err != nil {
			return err
		}
		kv = jsruntime.NewRamKv()
		activePlugins = cache.New(cache.NoExpiration, cache.NoExpiration)
		id2Path = cache.New(cache.NoExpiration, cache.NoExpiration)
		slog.Warn("Plugin module is not implemented yet.")

		return nil
	}))
	event.On(eventType.ServerInitializeStart, event.ListenerFunc(func(e event.Event) error {
		r := e.Get("engine").(*gin.Engine)
		// 获取所有插件信息
		r.GET("/api/admin/plugins", func(c *gin.Context) {
			manifests, err := getPluginManifest()
			if err != nil {
				resp.RespondError(c, 500, err.Error())
				return
			}
			resp.RespondSuccess(c, manifests)
		})
		// 激活指定插件，根据 ID 列表
		r.POST("/api/admin/plugin/activate", func(c *gin.Context) {
			var req struct {
				Id []string `json:"id"`
			}
			var err error
			if err := c.ShouldBindJSON(&req); err != nil {
				resp.RespondError(c, 400, "Invalid request.")
				return
			}
			for _, id := range req.Id {
				err = activePlugin(id)
				if err != nil {
					resp.RespondError(c, 500, err.Error())
					return
				}
			}
			resp.RespondSuccess(c, nil)
		})
		return nil
	}))
}

func getPluginManifest() ([]struct {
	Manifest
	Active   bool   `json:"active"`
	FileName string `json:"fileName"`
}, error) {
	var manifests []struct {
		Manifest
		Active   bool   `json:"active"`
		FileName string `json:"fileName"`
	}

	if id2Path != nil {
		id2Path.Flush()
	}

	entries, err := os.ReadDir("./data/plugins")
	if err != nil {
		slog.Error("failed to read plugin directory", slog.Any("err", err))
		return manifests, err
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".js" {
			continue
		}

		path := filepath.Join("./data/plugins", entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			slog.Error("failed to read plugin file", slog.String("file", path), slog.Any("err", err))
			continue
		}

		// 构建临时的 Runtime 用于读取 Manifest
		builder := jsruntime.NewBuilder().WithNodejs()
		if kv != nil {
			builder = builder.WithMemoryKv(kv)
		}

		rt, err := builder.Build()
		if err != nil {
			slog.Error("failed to build js runtime", slog.Any("err", err))
			continue
		}

		// 确保在循环结束时停止 Runtime，释放 EventLoop 线程
		// 使用匿名函数包装处理逻辑以便使用 defer (或者手动在 continue 前调用 Stop)
		func() {
			defer rt.Stop()

			if _, err := rt.RunScript(string(content)); err != nil {
				slog.Error("failed to execute plugin script", slog.String("file", path), slog.Any("err", err))
				return
			}

			// 使用 channel 从 EventLoop 线程同步获取 Manifest 数据
			type manifestResult struct {
				m   Manifest
				err error
			}
			ch := make(chan manifestResult, 1)

			rt.RunOnLoop(func(vm *goja.Runtime) {
				manifestVal := vm.Get("Manifest")
				if manifestVal == nil || goja.IsUndefined(manifestVal) || goja.IsNull(manifestVal) {
					ch <- manifestResult{err: fmt.Errorf("manifest not found or empty")}
					return
				}

				var m Manifest
				if err := vm.ExportTo(manifestVal, &m); err != nil {
					ch <- manifestResult{err: err}
					return
				}
				ch <- manifestResult{m: m}
			})

			res := <-ch
			if res.err != nil {
				slog.Warn("failed to parse plugin manifest", slog.String("file", path), slog.Any("err", res.err))
				return
			}
			manifest := res.m

			if id2Path != nil {
				id2Path.Set(manifest.Id, path, cache.NoExpiration)
			}

			active := false
			if _, found := activePlugins.Get(manifest.Id); found {
				active = true
			}
			manifests = append(manifests, struct {
				Manifest
				Active   bool   `json:"active"`
				FileName string `json:"fileName"`
			}{
				Manifest: manifest,
				Active:   active,
				FileName: entry.Name(),
			})
		}()
	}

	return manifests, nil
}

// activePlugin 激活指定 ID 的插件
func activePlugin(id string) error {
	if id2Path == nil {
		getPluginManifest() // reload manifest to populate id2Path
	}

	if _, found := activePlugins.Get(id); found {
		return nil // already active
	}
	pathVal, found := id2Path.Get(id)
	if !found {
		return fmt.Errorf("plugin not found.%s", id)
	}
	path := pathVal.(string)

	runtime, err := jsruntime.NewBuilder().WithNodejs().WithMemoryKv(kv).Build()
	if err != nil {
		return fmt.Errorf("failed to create js runtime for plugin %s(%s)", id, path)
	}

	// 定义清理函数，如果激活失败则停止 Runtime
	success := false
	defer func() {
		if !success {
			runtime.Stop()
		}
	}()

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read plugin file %s(%s)", id, path)
	}

	// 此时 RunScript 已经是线程安全并在 Loop 中执行的
	if _, err := runtime.RunScript(string(content)); err != nil {
		return fmt.Errorf("failed to execute plugin script %s(%s): %v", id, path, err)
	}

	// 将 Runtime 存入缓存
	activePlugins.Set(id, runtime, cache.NoExpiration)

	// 调用 onLoad
	// HasFunction 内部已经处理了 RunOnLoop
	if runtime.HasFunction("onLoad") {
		// Call 内部已经处理了 RunOnLoop
		_, err := runtime.Call("onLoad")
		if err != nil {
			activePlugins.Delete(id)
			return fmt.Errorf("failed to call onLoad for plugin %s(%s): %v", id, path, err)
		}
	}

	success = true
	return nil
}
