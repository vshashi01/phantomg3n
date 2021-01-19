package renderer

import "encoding/json"

// PhantomG3NMessage for client
type PhantomG3NMessage struct {
	Action string `json:"action"`
	Value  string `json:"value"`
}

// SendMessageToClient sends a message to the client
func (app *RenderingApp) SendMessageToClient(action string, value string) {
	m := &PhantomG3NMessage{Action: action, Value: value}
	msgJSON, err := json.Marshal(m)
	if err != nil {
		app.Application.Log().Error(err.Error())
		return
	}
	app.Log().Info("sending message: " + string(msgJSON))
	app.cImagestream <- []byte(string(msgJSON))
}
