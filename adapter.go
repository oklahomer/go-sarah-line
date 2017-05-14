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
	"strings"
	"time"
)

const (
	// LINE is a designated sara.BotType for LINE API interaction.
	LINE sarah.BotType = "line"
)

type EventHandler func(context.Context, *Config, []*linebot.Event, func(sarah.Input) error)

type Config struct {
	ChannelToken  string `json:"channel_token" yaml:"channel_token"`
	ChannelSecret string `json:"channel_secret" yaml:"channel_secret"`
	HelpCommand   string `json:"help_command" yaml:"help_command"`
	AbortCommand  string `json:"abort_command" yaml:"abort_command"`
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
		HelpCommand:   ".help",
		AbortCommand:  ".abort",
		Port:          8080,
		Endpoint:      "/callback",
		TLS:           nil,
		ClientOptions: nil,
	}
}

// AdapterOption defines function signature that Adapter's functional option must satisfy.
type AdapterOption func(adapter *Adapter) error

func WithClient(client *linebot.Client) AdapterOption {
	return func(adapter *Adapter) error {
		adapter.client = client
		return nil
	}
}

func WithEventhandler(handler EventHandler) AdapterOption {
	return func(adapter *Adapter) error {
		adapter.eventHandler = handler
		return nil
	}
}

type Adapter struct {
	client       *linebot.Client
	eventHandler EventHandler
	config       *Config
}

func NewAdapter(config *Config, options ...AdapterOption) (*Adapter, error) {
	adapter := &Adapter{
		config:       config,
		eventHandler: defaultEventHandler, // may be replaced with WithEventHandler option.
	}

	for _, opt := range options {
		err := opt(adapter)
		if err != nil {
			return nil, err
		}
	}

	// See if client is set by WithClient option.
	// If not, use given configuration
	if adapter.client == nil {
		client, err := linebot.New(config.ChannelSecret, config.ChannelToken, config.ClientOptions...)
		if err != nil {
			return nil, fmt.Errorf("error on linebot.Client construction: %s.", err.Error())
		}
		adapter.client = client
	}

	return adapter, nil
}

func (adapter *Adapter) BotType() sarah.BotType {
	return LINE
}

func (adapter *Adapter) Run(ctx context.Context, enqueueInput func(sarah.Input) error, notifyErr func(error)) {
	err := adapter.listen(ctx, enqueueInput)
	if err != nil {
		notifyErr(err)
	}
}

func (adapter *Adapter) SendMessage(ctx context.Context, output sarah.Output) {
	replyToken, ok := output.Destination().(string)
	if !ok {
		log.Errorf("destination is not string. %#v.", output.Destination())
		return
	}

	switch content := output.Content().(type) {
	case []linebot.Message:
		adapter.reply(ctx, replyToken, content)

	case linebot.Message:
		adapter.reply(ctx, replyToken, []linebot.Message{content})

	case *sarah.CommandHelps:
		messages := []linebot.Message{}
		for _, commandHelp := range *content {
			messages = append(messages, linebot.NewTextMessage(commandHelp.InputExample))
		}
		adapter.reply(ctx, replyToken, messages)

	default:
		log.Warnf("unexpected output %#v", output)
	}
}

func (adapter *Adapter) reply(ctx context.Context, replyToken string, message []linebot.Message) {
	call := adapter.client.ReplyMessage(replyToken, message...)
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	call.WithContext(reqCtx)
	_, err := call.Do()
	if err != nil {
		log.Errorf("error on message reply: %s", err.Error())
	}
}

func (adapter *Adapter) listen(ctx context.Context, enqueueInput func(sarah.Input) error) error {
	handler, err := httphandler.New(adapter.config.ChannelSecret, adapter.config.ChannelToken)
	if err != nil {
		return err
	}

	handler.HandleEvents(func(events []*linebot.Event, _ *http.Request) {
		adapter.eventHandler(ctx, adapter.config, events, enqueueInput)
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

func defaultEventHandler(_ context.Context, config *Config, events []*linebot.Event, enqueueInput func(sarah.Input) error) {
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

				trimmed := strings.TrimSpace(input.Message())
				if config.HelpCommand != "" && trimmed == config.HelpCommand {
					// Help command
					help := sarah.NewHelpInput(input.SenderKey(), input.Message(), input.SentAt(), input.ReplyTo())
					enqueueInput(help)

				} else if config.AbortCommand != "" && trimmed == config.AbortCommand {
					// Abort command
					abort := sarah.NewAbortInput(input.SenderKey(), input.Message(), input.SentAt(), input.ReplyTo())
					enqueueInput(abort)

				} else {
					// Regular input
					enqueueInput(input)

				}
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
	return NewCustomizedResponseWithNext(linebot.NewTextMessage(responseContent), nil)
}

func NewStringResponseWithNext(responseContent string, next sarah.ContextualFunc) *sarah.CommandResponse {
	return NewCustomizedResponseWithNext(linebot.NewTextMessage(responseContent), next)
}

func NewCustomizedResponse(responseMessage linebot.Message) *sarah.CommandResponse {
	return NewCustomizedResponseWithNext(responseMessage, nil)
}

func NewCustomizedResponseWithNext(responseMessage linebot.Message, next sarah.ContextualFunc) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content:     responseMessage,
		UserContext: sarah.NewUserContext(next),
	}
}

func NewMultipleCustomizedResponses(responseMessages []linebot.Message) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content:     responseMessages,
		UserContext: nil,
	}
}

func NewMultipleCustomizedResponsesWithNext(responseMessages []linebot.Message, next sarah.ContextualFunc) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content:     responseMessages,
		UserContext: sarah.NewUserContext(next),
	}
}
