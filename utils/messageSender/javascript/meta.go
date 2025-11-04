package javascript

type Addition struct {
	Script string `json:"script" required:"true" type:"richtext" help:"JavaScript code (ES6 supported) that implements sendMessage(message, title) and optionally sendEvent(event) functions. Both should return a Promise or boolean. Available APIs: fetch(), xhr(), console.log()."`
}
