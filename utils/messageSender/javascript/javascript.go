package javascript

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils/messageSender/factory"
)

type JavaScriptSender struct {
	Addition
	vm          *goja.Runtime
	noopProgram *goja.Program
}

func (j *JavaScriptSender) GetName() string {
	return "Javascript"
}

func (j *JavaScriptSender) GetConfiguration() factory.Configuration {
	return &j.Addition
}

func (j *JavaScriptSender) Init() error {
	if j.Addition.Script == "" {
		return errors.New("JavaScript script is empty")
	}

	// 创建 JavaScript 运行时
	j.vm = goja.New()

	// 预编译一个 no-op 程序,用于驱动微任务队列
	prog, errc := goja.Compile("noop.js", "void 0", false)
	if errc == nil {
		j.noopProgram = prog
	}

	// 设置 require 支持
	new(require.Registry).Enable(j.vm)

	// 注入全局对象和函数
	j.setupGlobals()

	// 加载用户脚本
	_, err := j.vm.RunString(j.Addition.Script)
	if err != nil {
		return fmt.Errorf("failed to load JavaScript script: %v", err)
	}

	// 验证 sendMessage 函数是否存在
	sendMessage := j.vm.Get("sendMessage")
	if sendMessage == nil || goja.IsUndefined(sendMessage) {
		return errors.New("sendMessage function not defined in script")
	}

	// 验证是否可调用
	if _, ok := goja.AssertFunction(sendMessage); !ok {
		return errors.New("sendMessage is not a function")
	}

	// sendEvent 函数是可选的,不强制要求存在

	return nil
}

func (j *JavaScriptSender) Destroy() error {
	if j.vm != nil {
		j.vm = nil
	}
	return nil
}

func (j *JavaScriptSender) SendTextMessage(message, title string) error {
	if j.vm == nil {
		if err := j.Init(); err != nil {
			return err
		}
	}

	// 获取 sendMessage 函数
	sendMessageFunc, ok := goja.AssertFunction(j.vm.Get("sendMessage"))
	if !ok {
		return errors.New("sendMessage is not a callable function")
	}

	// 调用 sendMessage 函数
	resultChan := make(chan error, 1)
	timeoutChan := time.After(30 * time.Second)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				resultChan <- fmt.Errorf("JavaScript panic: %v", r)
			}
		}()

		result, err := sendMessageFunc(goja.Undefined(), j.vm.ToValue(message), j.vm.ToValue(title))
		if err != nil {
			resultChan <- fmt.Errorf("JavaScript error: %v", err)
			return
		}

		// 处理 Promise 返回值
		if promise, ok := result.Export().(*goja.Promise); ok {
			// 等待 Promise 完成
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-timeoutChan:
					resultChan <- errors.New("JavaScript execution timeout")
					return
				case <-ticker.C:
					// 运行微任务以处理 Promise 回调
					j.runMicrotasks()

					state := promise.State()
					if state == goja.PromiseStateFulfilled {
						// Promise 成功完成,检查返回值
						promiseResult := promise.Result()
						if !promiseResult.ToBoolean() {
							resultChan <- errors.New("sendMessage returned false")
						} else {
							resultChan <- nil
						}
						return
					} else if state == goja.PromiseStateRejected {
						// Promise 被拒绝
						resultChan <- fmt.Errorf("Promise rejected: %v", promise.Result())
						return
					}
					// state == goja.PromiseStatePending, 继续等待
				}
			}
		} else {
			// 处理布尔或其他返回值
			if result.ToBoolean() {
				resultChan <- nil
			} else {
				resultChan <- errors.New("sendMessage returned false")
			}
		}
	}()

	select {
	case err := <-resultChan:
		return err
	case <-timeoutChan:
		return errors.New("JavaScript execution timeout after 30 seconds")
	}
}

func (j *JavaScriptSender) SendEvent(event models.EventMessage) error {
	if j.vm == nil {
		if err := j.Init(); err != nil {
			return err
		}
	}

	// 检查是否定义了 sendEvent 函数
	sendEventValue := j.vm.Get("sendEvent")
	if sendEventValue == nil || goja.IsUndefined(sendEventValue) {
		// 如果没有定义 sendEvent,则回退到使用 SendTextMessage
		return j.fallbackToTextMessage(event)
	}

	sendEventFunc, ok := goja.AssertFunction(sendEventValue)
	if !ok {
		// 如果 sendEvent 不是函数,回退到 SendTextMessage
		return j.fallbackToTextMessage(event)
	}

	// 将 EventMessage 转换为 JavaScript 对象
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %v", err)
	}

	var eventMap map[string]interface{}
	if err := json.Unmarshal(eventJSON, &eventMap); err != nil {
		return fmt.Errorf("failed to unmarshal event: %v", err)
	}

	// 调用 sendEvent 函数
	resultChan := make(chan error, 1)
	timeoutChan := time.After(30 * time.Second)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				resultChan <- fmt.Errorf("JavaScript panic: %v", r)
			}
		}()

		result, err := sendEventFunc(goja.Undefined(), j.vm.ToValue(eventMap))
		if err != nil {
			resultChan <- fmt.Errorf("JavaScript error: %v", err)
			return
		}

		// 处理 Promise 返回值
		if promise, ok := result.Export().(*goja.Promise); ok {
			// 等待 Promise 完成
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-timeoutChan:
					resultChan <- errors.New("JavaScript execution timeout")
					return
				case <-ticker.C:
					// 运行微任务以处理 Promise 回调
					j.runMicrotasks()

					state := promise.State()
					if state == goja.PromiseStateFulfilled {
						// Promise 成功完成,检查返回值
						promiseResult := promise.Result()
						if !promiseResult.ToBoolean() {
							resultChan <- errors.New("sendEvent returned false")
						} else {
							resultChan <- nil
						}
						return
					} else if state == goja.PromiseStateRejected {
						// Promise 被拒绝
						resultChan <- fmt.Errorf("Promise rejected: %v", promise.Result())
						return
					}
					// state == goja.PromiseStatePending, 继续等待
				}
			}
		} else {
			// 处理布尔或其他返回值
			if result.ToBoolean() {
				resultChan <- nil
			} else {
				resultChan <- errors.New("sendEvent returned false")
			}
		}
	}()

	select {
	case err := <-resultChan:
		return err
	case <-timeoutChan:
		return errors.New("JavaScript execution timeout after 30 seconds")
	}
}

// fallbackToTextMessage 当没有定义 sendEvent 时,回退到使用文本消息格式
func (j *JavaScriptSender) fallbackToTextMessage(event models.EventMessage) error {
	// 构建简单的文本消息
	message := fmt.Sprintf("%s%s%s\nEvent: %s\nMessage: %s\nTime: %s",
		event.Emoji, event.Emoji, event.Emoji,
		event.Event,
		event.Message,
		event.Time.Format(time.RFC3339))

	// 添加客户端信息
	if len(event.Clients) > 0 {
		clientNames := make([]string, 0, len(event.Clients))
		for _, c := range event.Clients {
			name := c.Name
			if name == "" {
				name = c.UUID
			}
			clientNames = append(clientNames, name)
		}
		message = fmt.Sprintf("%s%s%s\nEvent: %s\nClients: %s\nMessage: %s\nTime: %s",
			event.Emoji, event.Emoji, event.Emoji,
			event.Event,
			clientNames,
			event.Message,
			event.Time.Format(time.RFC3339))
	}

	return j.SendTextMessage(message, event.Event)
}

// runMicrotasks 安全地推动 goja 的微任务队列(例如 Promise 回调)
func (j *JavaScriptSender) runMicrotasks() {
	if j.vm == nil {
		return
	}
	if j.noopProgram != nil {
		_, _ = j.vm.RunProgram(j.noopProgram)
		return
	}
	// 兜底: 直接运行一段 no-op 代码
	_, _ = j.vm.RunString("void 0")
}

func (j *JavaScriptSender) setupGlobals() {
	// 注入 console.log
	console := j.vm.NewObject()
	console.Set("log", func(call goja.FunctionCall) goja.Value {
		var args []interface{}
		for _, arg := range call.Arguments {
			args = append(args, arg.Export())
		}
		fmt.Println(args...)
		return goja.Undefined()
	})
	console.Set("error", func(call goja.FunctionCall) goja.Value {
		fmt.Print("Error: ")
		for i, arg := range call.Arguments {
			if i > 0 {
				fmt.Print(" ")
			}
			fmt.Print(arg.Export())
		}
		fmt.Println()
		return goja.Undefined()
	})
	j.vm.Set("console", console)

	// 注入 fetch API
	j.vm.Set("fetch", j.createFetchFunction())

	// 注入 XMLHttpRequest (xhr)
	j.vm.Set("XMLHttpRequest", j.createXHRConstructor())

	// 注入 setTimeout
	j.vm.Set("setTimeout", func(call goja.FunctionCall) goja.Value {
		callback := call.Argument(0)
		delay := call.Argument(1).ToInteger()

		go func() {
			time.Sleep(time.Duration(delay) * time.Millisecond)
			if fn, ok := goja.AssertFunction(callback); ok {
				fn(goja.Undefined())
			}
		}()

		return goja.Undefined()
	})

	// 注入 Promise 构造函数
	j.vm.RunString(`
		if (typeof Promise === 'undefined') {
			// Promise polyfill 会由 goja 自动提供
		}
	`)
}

func init() {
	factory.RegisterMessageSender(func() factory.IMessageSender {
		return &JavaScriptSender{}
	})
}

// 确保实现了 IMessageSender 接口
var _ factory.IMessageSender = (*JavaScriptSender)(nil)
