package javascript

type Addition struct {
	Script string `json:"script" name:"Script" required:"true" type:"richtext" help:"Implement sendMessage(message, title) and optionally sendEvent(event). Return true or a Promise resolving to true. Available APIs include fetch, XMLHttpRequest, console, timers, Buffer and CommonJS require with Node compatibility modules. Files and local modules are confined to data/notification/javascript; command execution and listening on ports are disabled."`
}
