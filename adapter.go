package line

import (
	"fmt"
	"github.com/line/line-bot-sdk-go/linebot"
	"github.com/line/line-bot-sdk-go/linebot/httphandler"
	"github.com/oklahomer/go-sarah"
	"github.com/oklahomer/go-sarah/log"
	"golang.org/x/net/context"
	"net/http"
	"net/http/httputil"
	"time"
)

const (
	// LINE is a designated sara.BotType for LINE API interaction.
	LINE sarah.BotType = "line"
)

type EventHandler func(*linebot.Client, []*linebot.Event, chan<- sarah.Input)

type Config struct {
	ChannelToken  string `json:"channel_token" yaml:"channel_token"`
	ChannelSecret string `json:"channel_secret" yaml:"channel_secret"`
	Port          int    `json:"port" yaml:"port"`
	Endpoint      string `json:"endpoint" yaml:"endpoint"`
	TLS           *struct {
		CertFile string `json:"cert_file" yaml:"cert_file"`
		KeyFile  string `json:"key_file" yaml:"key_file"`
	} `json:"tls" yaml:"tls"`
	ClientOptions []linebot.ClientOption
}

// NewConfig returns initialized Config struct with default settings.
// Token is empty at this point. Token can be set by feeding this instance to json.Unmarshal/yaml.Unmarshal,
// or direct assignment.
func NewConfig() *Config {
	return &Config{
		ChannelToken:  "",
		ChannelSecret: "",
		Port:          8080,
		Endpoint:      "/callback",
		TLS:           nil,
		ClientOptions: nil,
	}
}

type Adapter struct {
	client       *linebot.Client
	eventHandler EventHandler
	config       *Config
}

func NewAdapter(config *Config) *Adapter {
	client, err := linebot.New(config.ChannelSecret, config.ChannelToken, config.ClientOptions...)
	if err != nil {
		panic(fmt.Sprintf("Error on linebot.Client construction: %s", err.Error()))
	}

	return &Adapter{
		client:       client,
		eventHandler: defaultEventHandler,
		config:       config,
	}
}

func NewAdapterWithHandler(config *Config, handler EventHandler) *Adapter {
	adapter := NewAdapter(config)
	adapter.eventHandler = handler
	return adapter
}

func (adapter *Adapter) BotType() sarah.BotType {
	return LINE
}

func (adapter *Adapter) Run(ctx context.Context, receivedMessage chan<- sarah.Input, errNotifier func(error)) {
	err := adapter.listen(receivedMessage)
	if err != nil {
		errNotifier(err)
	}
}

func (adapter *Adapter) SendMessage(ctx context.Context, output sarah.Output) {
	replyToken, ok := output.Destination().(string)
	if !ok {
		log.Errorf("destination is not string. %#v.", output.Destination())
		return
	}

	switch content := output.Content().(type) {
	case string:
		adapter.reply(ctx, replyToken, linebot.NewTextMessage(content))
	case linebot.Message:
		adapter.reply(ctx, replyToken, content)
	default:
		log.Warnf("unexpected output %#v", output)
	}
}

func (adapter *Adapter) reply(ctx context.Context, replyToken string, message linebot.Message) {
	call := adapter.client.ReplyMessage(replyToken, message)
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	call.WithContext(reqCtx)
	_, err := call.Do()
	if err != nil {
		log.Errorf("error on message reply: %s", err.Error())
	}
}

func (adapter *Adapter) listen(receivedMessage chan<- sarah.Input) error {
	handler, err := httphandler.New(adapter.config.ChannelSecret, adapter.config.ChannelToken)
	if err != nil {
		return err
	}

	handler.HandleEvents(func(events []*linebot.Event, _ *http.Request) {
		adapter.eventHandler(adapter.client, events, receivedMessage)
	})
	handler.HandleError(func(err error, req *http.Request) {
		dump, dumpErr := httputil.DumpRequest(req, true)
		if dumpErr == nil {
			log.Errorf("error on reqeust parsing and/or signature validation. error: %s. request: %s.", err.Error(), dump)
		} else {
			log.Errorf("error on reqeust parsing and/or signature validation: %s. request: %s.", err.Error())
		}
	})

	http.Handle(adapter.config.Endpoint, handler)
	addr := fmt.Sprintf(":%d", adapter.config.Port)
	if adapter.config.TLS == nil {
		return http.ListenAndServe(addr, nil)
	} else {
		return http.ListenAndServeTLS(addr, adapter.config.TLS.CertFile, adapter.config.TLS.KeyFile, nil)
	}
}

func defaultEventHandler(_ *linebot.Client, events []*linebot.Event, receivedMessage chan<- sarah.Input) {
	for _, event := range events {
		if event.Type == linebot.EventTypeMessage {
			switch message := event.Message.(type) {
			case *linebot.TextMessage:
				var senderKey string
				if event.Source.Type == linebot.EventSourceTypeUser {
					senderKey = fmt.Sprintf("user|%s", event.Source.UserID)
				} else if event.Source.Type == linebot.EventSourceTypeRoom {
					senderKey = fmt.Sprintf("room|%s", event.Source.RoomID)
				} else if event.Source.Type == linebot.EventSourceTypeGroup {
					senderKey = fmt.Sprintf("group|%s", event.Source.GroupID)
				} else {
					log.Errorf("Unrecognized event source type: %s", event.Source.Type)
					continue
				}

				input := &TextInput{
					senderKey:  senderKey,
					text:       message.Text,
					replyToken: event.ReplyToken,
					timestamp:  event.Timestamp,
				}
				receivedMessage <- input
			}
		}
	}
}

// TextInput satisfies sarah.Input interface
type TextInput struct {
	senderKey  string
	text       string
	replyToken string
	timestamp  time.Time
}

func (input *TextInput) SenderKey() string {
	return input.senderKey
}

func (input *TextInput) Message() string {
	return input.text
}

func (input *TextInput) SentAt() time.Time {
	return input.timestamp
}

func (input *TextInput) ReplyTo() sarah.OutputDestination {
	return input.replyToken
}

func NewStringResponse(responseContent string) *sarah.CommandResponse {
	return NewStringResponseWithNext(responseContent, nil)
}

func NewStringResponseWithNext(responseContent string, next sarah.ContextualFunc) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content: responseContent,
		Next:    next,
	}
}

func NewCustomizedResponse(responseMessage linebot.Message) *sarah.CommandResponse {
	return NewCustomizedResponseWithNext(responseMessage, nil)
}

func NewCustomizedResponseWithNext(responseMessage linebot.Message, next sarah.ContextualFunc) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content: responseMessage,
		Next:    next,
	}
}
