package line

import (
	"context"
	"errors"
	"fmt"
	"github.com/line/line-bot-sdk-go/linebot"
	"github.com/line/line-bot-sdk-go/linebot/httphandler"
	"github.com/oklahomer/go-sarah/v2"
	"github.com/oklahomer/go-sarah/v2/log"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

const (
	// LINE is a designated sara.BotType for LINE API interaction.
	LINE sarah.BotType = "line"
)

// Config contains some configuration variables for line Adapter.
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

// WithClient creates AdapterOption with given *linebot.Client.
func WithClient(client *linebot.Client) AdapterOption {
	return func(adapter *Adapter) error {
		adapter.client = client
		return nil
	}
}

// WithEventHandler creates AdapterOption with given function.
// This function is called on event reception.
func WithEventHandler(handler func(context.Context, *Config, []*linebot.Event, func(sarah.Input) error)) AdapterOption {
	return func(adapter *Adapter) error {
		adapter.eventHandler = handler
		return nil
	}
}

// Adapter internally starts HTTP server to receive call from LINE.
type Adapter struct {
	client       *linebot.Client
	eventHandler func(context.Context, *Config, []*linebot.Event, func(sarah.Input) error)
	config       *Config
}

var _ sarah.Adapter = (*Adapter)(nil)

// NewAdapter creates new Adapter with given *Config and zero or more AdapterOption.
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
			return nil, fmt.Errorf("error on linebot.Client construction: %s", err.Error())
		}
		adapter.client = client
	}

	return adapter, nil
}

// BotType returns BotType of this particular instance.
func (adapter *Adapter) BotType() sarah.BotType {
	return LINE
}

// Run starts HTTP server to handle incoming request from LINE.
func (adapter *Adapter) Run(ctx context.Context, enqueueInput func(sarah.Input) error, notifyErr func(error)) {
	err := adapter.listen(ctx, enqueueInput)
	if err != nil {
		notifyErr(err)
	}
}

// SendMessage let Bot send message to LINE.
func (adapter *Adapter) SendMessage(ctx context.Context, output sarah.Output) {
	replyToken, ok := output.Destination().(string)
	if !ok {
		log.Errorf("destination is not string. %#v.", output.Destination())
		return
	}

	switch content := output.Content().(type) {
	case []linebot.SendingMessage:
		adapter.reply(ctx, replyToken, content)

	case linebot.SendingMessage:
		adapter.reply(ctx, replyToken, []linebot.SendingMessage{content})

	case *sarah.CommandHelps:
		var messages []linebot.SendingMessage
		for _, commandHelp := range *content {
			messages = append(messages, linebot.NewTextMessage(commandHelp.Instruction))
		}
		adapter.reply(ctx, replyToken, messages)

	default:
		log.Warnf("unexpected output %#v", output)
	}
}

func (adapter *Adapter) reply(ctx context.Context, replyToken string, message []linebot.SendingMessage) {
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
			log.Errorf("error on request parsing and/or signature validation. error: %s. request: %s.", err.Error(), dump)
		} else {
			log.Errorf("error on request parsing and/or signature validation: %s.", err.Error())
		}
	})

	http.Handle(adapter.config.Endpoint, handler)
	addr := fmt.Sprintf(":%d", adapter.config.Port)
	if adapter.config.TLS == nil {
		return http.ListenAndServe(addr, nil)
	}

	return http.ListenAndServeTLS(addr, adapter.config.TLS.CertFile, adapter.config.TLS.KeyFile, nil)
}

func defaultEventHandler(_ context.Context, config *Config, events []*linebot.Event, enqueueInput func(sarah.Input) error) {
	for _, event := range events {
		if event.Type == linebot.EventTypeMessage || event.Type == linebot.EventTypePostback {
			input, err := EventToUserInput(config, event)
			if err != nil {
				log.Errorf("Error on event handling: %s.", err.Error())
				continue
			}

			enqueueInput(input)
		}
	}
}

// EventToUserInput converts linebot.Event to a corresponding struct that implements sarah.Input.
//
// This does not treat Follow, Unfollow, Join, Leave, or Beacon as *user input*.
// It is nonsense to pass uniformed state change event to sarah.Commands and find corresponding sarah.Command.
// To handle those events, pass customized event handler on Adapter construction via WithEventHandler.
func EventToUserInput(config *Config, event *linebot.Event) (sarah.Input, error) {
	sourceType := event.Source.Type
	senderKey, err := SourceToSenderKey(event.Source)
	if err != nil {
		return nil, err
	}

	if event.Type == linebot.EventTypeMessage {
		switch message := event.Message.(type) {
		case *linebot.TextMessage:
			input := &TextInput{
				sourceType: sourceType,
				ID:         message.ID,
				senderKey:  senderKey,
				text:       message.Text,
				replyToken: event.ReplyToken,
				timestamp:  event.Timestamp,
			}

			trimmed := strings.TrimSpace(message.Text)
			if config.HelpCommand != "" && trimmed == config.HelpCommand {
				// Help command
				return sarah.NewHelpInput(input), nil

			} else if config.AbortCommand != "" && trimmed == config.AbortCommand {
				// Abort command
				return sarah.NewAbortInput(input), nil

			}

			return input, nil

		case *linebot.ImageMessage:
			return &FileInput{
				sourceType: sourceType,
				Type:       linebot.MessageTypeImage,
				ID:         message.ID,
				senderKey:  senderKey,
				replyToken: event.ReplyToken,
				timestamp:  event.Timestamp,
			}, nil

		case *linebot.VideoMessage:
			return &FileInput{
				sourceType: sourceType,
				Type:       linebot.MessageTypeVideo,
				ID:         message.ID,
				senderKey:  senderKey,
				replyToken: event.ReplyToken,
				timestamp:  event.Timestamp,
			}, nil

		case *linebot.AudioMessage:
			return &FileInput{
				sourceType: sourceType,
				Type:       linebot.MessageTypeAudio,
				ID:         message.ID,
				senderKey:  senderKey,
				replyToken: event.ReplyToken,
				timestamp:  event.Timestamp,
			}, nil

		case *linebot.LocationMessage:
			return &LocationInput{
				sourceType: sourceType,
				ID:         message.ID,
				Location: &Location{
					Title:     message.Title,
					Address:   message.Address,
					Latitude:  message.Latitude,
					Longitude: message.Longitude,
				},
				senderKey:  senderKey,
				replyToken: event.ReplyToken,
				timestamp:  event.Timestamp,
			}, nil

		case *linebot.StickerMessage:
			return &StickerInput{
				sourceType: sourceType,
				ID:         message.ID,

				PackageID: message.PackageID,
				StickerID: message.StickerID,

				senderKey:  senderKey,
				replyToken: event.ReplyToken,
				timestamp:  event.Timestamp,
			}, nil

		default:
			return nil, fmt.Errorf("unknown message type: %T", event.Message)
		}

	} else if event.Type == linebot.EventTypePostback {
		postback := event.Postback
		var params *PostbackParams
		if postback.Params != nil {
			params = &PostbackParams{
				Date:     postback.Params.Date,
				Time:     postback.Params.Time,
				Datetime: postback.Params.Datetime,
			}
		}
		input := &PostbackEvent{
			Params:     params,
			sourceType: sourceType,
			senderKey:  senderKey,
			data:       postback.Data,
			replyToken: event.ReplyToken,
			timestamp:  event.Timestamp,
		}

		trimmed := strings.TrimSpace(input.Message())
		if config.HelpCommand != "" && trimmed == config.HelpCommand {
			// Help command
			return sarah.NewHelpInput(input), nil

		} else if config.AbortCommand != "" && trimmed == config.AbortCommand {
			// Abort command
			return sarah.NewAbortInput(input), nil

		}

		return input, nil
	}

	return nil, fmt.Errorf("%T can not be treated as user input", event)
}

// ErrUnrecognizedEventSource indicates unrecognizable linebot.EventSourceType.
var ErrUnrecognizedEventSource = errors.New("unrecognized event source type is given")

// SourceToSenderKey generates unique sender key from given event.
// https://devdocs.line.me/en/#webhook-event-object
func SourceToSenderKey(s *linebot.EventSource) (string, error) {
	switch s.Type {
	case linebot.EventSourceTypeUser:
		return fmt.Sprintf("user|%s", s.UserID), nil

	case linebot.EventSourceTypeRoom:
		return fmt.Sprintf("room|%s", s.RoomID), nil

	case linebot.EventSourceTypeGroup:
		return fmt.Sprintf("group|%s", s.GroupID), nil

	default:
		return "", ErrUnrecognizedEventSource

	}
}

// TextInput represents text message sent from LINE.
type TextInput struct {
	ID string

	sourceType linebot.EventSourceType
	senderKey  string
	text       string
	replyToken string
	timestamp  time.Time
}

// SenderKey returns string representing message sender.
func (input *TextInput) SenderKey() string {
	return input.senderKey
}

// Message returns sent message.
func (input *TextInput) Message() string {
	return input.text
}

// SentAt returns message event's timestamp.
func (input *TextInput) SentAt() time.Time {
	return input.timestamp
}

// ReplyTo returns token to send reply.
func (input *TextInput) ReplyTo() sarah.OutputDestination {
	return input.replyToken
}

// SourceType returns this event's linebot.EventSourceType.
// All events in LINE Adapter implement SourceTyper, so this is safe to apply type assertion against sarah.Input and see corresponding source type.
func (input *TextInput) SourceType() linebot.EventSourceType {
	return input.sourceType
}

// FileInput represents file message sent from LINE.
type FileInput struct {
	// Type is one of MessageTypeImage, MessageTypeVideo, MessageTypeAudio
	Type linebot.MessageType
	ID   string

	sourceType linebot.EventSourceType
	senderKey  string
	replyToken string
	timestamp  time.Time
}

// SenderKey returns string representing message sender.
func (input *FileInput) SenderKey() string {
	return input.senderKey
}

// Message returns sent message, which is empty in this case.
func (input *FileInput) Message() string {
	return ""
}

// SentAt returns message event's timestamp.
func (input *FileInput) SentAt() time.Time {
	return input.timestamp
}

// ReplyTo returns token to send reply.
func (input *FileInput) ReplyTo() sarah.OutputDestination {
	return input.replyToken
}

// SourceType returns this event's linebot.EventSourceType.
// All events in LINE Adapter implement SourceTyper, so this is safe to apply type assertion against sarah.Input and see corresponding source type.
func (input *FileInput) SourceType() linebot.EventSourceType {
	return input.sourceType
}

// Location represents location being sent.
type Location struct {
	Title     string
	Address   string
	Latitude  float64
	Longitude float64
}

// LocationInput represents location message sent from LINE.
type LocationInput struct {
	ID       string
	Location *Location

	sourceType linebot.EventSourceType
	senderKey  string
	replyToken string
	timestamp  time.Time
}

// SenderKey returns string representing message sender.
func (input *LocationInput) SenderKey() string {
	return input.senderKey
}

// Message returns sent message.
func (input *LocationInput) Message() string {
	return input.Location.Title
}

// SentAt returns message event's timestamp.
func (input *LocationInput) SentAt() time.Time {
	return input.timestamp
}

// ReplyTo returns token to send reply.
func (input *LocationInput) ReplyTo() sarah.OutputDestination {
	return input.replyToken
}

// SourceType returns this event's linebot.EventSourceType.
// All events in LINE Adapter implement SourceTyper, so this is safe to apply type assertion against sarah.Input and see corresponding source type.
func (input *LocationInput) SourceType() linebot.EventSourceType {
	return input.sourceType
}

// StickerInput represents sticker message sent from LINE.
type StickerInput struct {
	ID        string
	PackageID string
	StickerID string

	sourceType linebot.EventSourceType
	senderKey  string
	replyToken string
	timestamp  time.Time
}

// SenderKey returns string representing message sender.
func (input *StickerInput) SenderKey() string {
	return input.senderKey
}

// Message returns sent message, which is empty in this case.
func (input *StickerInput) Message() string {
	return ""
}

// SentAt returns message event's timestamp.
func (input *StickerInput) SentAt() time.Time {
	return input.timestamp
}

// ReplyTo returns token to send reply.
func (input *StickerInput) ReplyTo() sarah.OutputDestination {
	return input.replyToken
}

// SourceType returns this event's linebot.EventSourceType.
// All events in LINE Adapter implement SourceTyper, so this is safe to apply type assertion against sarah.Input and see corresponding source type.
func (input *StickerInput) SourceType() linebot.EventSourceType {
	return input.sourceType
}

// PostbackParams includes some datetime related parameters set by user.
// This is set when and only when user picks datetime via datetime picker action.
//
// ref. https://developers.line.me/en/docs/messaging-api/reference/#postback-params-object
type PostbackParams struct {
	Date     string
	Time     string
	Datetime string
}

// PostbackEvent represents postback event sent from LINE.
type PostbackEvent struct {
	Params *PostbackParams

	sourceType linebot.EventSourceType
	senderKey  string
	data       string
	replyToken string
	timestamp  time.Time
}

// SenderKey returns string representing message sender.
func (input *PostbackEvent) SenderKey() string {
	return input.senderKey
}

// Message returns sent message.
func (input *PostbackEvent) Message() string {
	return input.data
}

// SentAt returns message event's timestamp.
func (input *PostbackEvent) SentAt() time.Time {
	return input.timestamp
}

// ReplyTo returns token to send reply.
func (input *PostbackEvent) ReplyTo() sarah.OutputDestination {
	return input.replyToken
}

// SourceType returns this event's linebot.EventSourceType.
// All events in LINE Adapter implement SourceTyper, so this is safe to apply type assertion against sarah.Input and see corresponding source type.
func (input *PostbackEvent) SourceType() linebot.EventSourceType {
	return input.sourceType
}

// SourceTyper is an interface that returns event's linebot.EventSourceType
type SourceTyper interface {
	SourceType() linebot.EventSourceType
}

// Make sure All input types implements SourceTyper and sarah.Input
var _ SourceTyper = (*TextInput)(nil)
var _ SourceTyper = (*FileInput)(nil)
var _ SourceTyper = (*StickerInput)(nil)
var _ SourceTyper = (*LocationInput)(nil)
var _ SourceTyper = (*PostbackEvent)(nil)
var _ sarah.Input = (*TextInput)(nil)
var _ sarah.Input = (*FileInput)(nil)
var _ sarah.Input = (*StickerInput)(nil)
var _ sarah.Input = (*LocationInput)(nil)
var _ sarah.Input = (*PostbackEvent)(nil)

// IsSourceUser checks given input and return true if the given input sender is user.
func IsSourceUser(input interface{}) bool {
	typer, ok := input.(SourceTyper)
	if !ok {
		return false
	}

	return typer.SourceType() == linebot.EventSourceTypeUser
}

// IsSourceRoom checks given input and return true if the given input sender is room.
func IsSourceRoom(input interface{}) bool {
	typer, ok := input.(SourceTyper)
	if !ok {
		return false
	}

	return typer.SourceType() == linebot.EventSourceTypeRoom
}

// IsSourceGroup checks given input and return true if the given input sender is group.
func IsSourceGroup(input interface{}) bool {
	typer, ok := input.(SourceTyper)
	if !ok {
		return false
	}

	return typer.SourceType() == linebot.EventSourceTypeGroup
}

// NewStringResponse creates new sarah.CommandResponse instance with given string.
func NewStringResponse(responseContent string) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content:     linebot.NewTextMessage(responseContent),
		UserContext: nil,
	}
}

// NewStringResponseWithNext creates new sarah.CommandResponse instance with given string and next function to continue.
func NewStringResponseWithNext(responseContent string, next sarah.ContextualFunc) *sarah.CommandResponse {
	return NewCustomizedResponseWithNext(linebot.NewTextMessage(responseContent), next)
}

// NewCustomizedResponse creates new sarah.CommandResponse instance with given linebot.Message.
func NewCustomizedResponse(responseMessage linebot.Message) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content:     responseMessage,
		UserContext: nil,
	}
}

// NewCustomizedResponseWithNext creates new sarah.CommandResponse instance with given linebot.Message and next function to continue.
func NewCustomizedResponseWithNext(responseMessage linebot.Message, next sarah.ContextualFunc) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content:     responseMessage,
		UserContext: sarah.NewUserContext(next),
	}
}

// NewMultipleCustomizedResponses creates new sarah.CommandResponse instance with given []linebot.Message.
func NewMultipleCustomizedResponses(responseMessages []linebot.Message) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content:     responseMessages,
		UserContext: nil,
	}
}

// NewMultipleCustomizedResponsesWithNext creates new sarah.CommandResponse instance with given []linebot.Message with next function to continue.
func NewMultipleCustomizedResponsesWithNext(responseMessages []linebot.Message, next sarah.ContextualFunc) *sarah.CommandResponse {
	return &sarah.CommandResponse{
		Content:     responseMessages,
		UserContext: sarah.NewUserContext(next),
	}
}
