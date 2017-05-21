This is a [sarah](https://github.com/oklahomer/go-sarah) ```Adapter``` implementation for [LINE Messaging API](https://developers.line.me/messaging-api/overview).

# Recommended storage
## Idea of user's conversational state: UserContext
One outstanding feature that ```Sarah``` offers is the ability to store user's conversational context, ```sarah.UserContext```, initiated by ```sarah.Command```.
With this feature, ```sarah.Command``` developer can let messaging user stay in the ```sarah.Command```'s conversation without adding any change to ```sarah.Bot``` or ```sarah.Runner``` logic.

When a ```sarah.Command``` returns ```sarah.CommandResponose``` with ```sarah.UserContext```,
```Sarah``` considers the user is in the middle of ```sarah.Command's``` conversational context and stores this context information in designated ```sarah.Storage```.
When the user sends next input within a pre-configured timeout window, the input is passed to the function defined by stored ```sarah.UserContext```.

This is how a ```sarah.Command``` turns typical one-response-per-input bot interaction to conversational one so users can input series of arguments in a more user-friendly conversational manner.

## sarah.UserContextStorage
Pre-defined default storage is provided and can be initialized via ```sarah.NewUserContextStorage```, but developers may replace it with preferred storage since ```sarah.UserContextStorage``` is merely an interface.
A use of alternative storage is indeed recommended for production environment for two reasons:

- Default storage internally uses a map to store ```sarah.UserContext``` in the process memory space, which means all stored contexts are vanished on process restart.
- While some chat services such as Slack and Gitter let bot initiate a connection against chat server, LINE Messaging API lets LINE initiate HTTP request against bot server.
With this model, to handle larger amount of HTTP requests, bot may consist of multiple server instances.
Therefore multiple ```sarah.Bot``` processes over multiple server instances must be capable of sharing ```sarah.UserContextStorage``` to let user continue her conversation.

Currently [go-sarah-rediscontext](https://github.com/oklahomer/go-sarah-rediscontext) is provided as one solution to store serialized ```sarah.UserContext``` in Redis.
One limitation to use external KVS is that arguments to the callback function must be serializable;
while default storage does not require so because it casually stores callback functions in Golang's map structure.

Other implementations for different storage middleware should be available by implementing ```sarah.UserContextStorage``` interface.

# Getting Started
Below is a minimal sample that describes how to setup and start LINE Adapter.
For more detailed description how to register ```sarah.Command``` and behaviors, see [Sarah's README.md](https://github.com/oklahomer/go-sarah) and its [example code](https://github.com/oklahomer/go-sarah/tree/master/examples).

```go
package main

import (
        "github.com/oklahomer/go-sarah"
        "github.com/oklahomer/go-sarah-line"
        "golang.org/x/net/context"
        "gopkg.in/yaml.v2"
        "io/ioutil"
)

func main() {
        // Setup configuration
        configBuf, _ := ioutil.ReadFile("/path/to/adapter/config.yaml")
        lineConfig := line.NewConfig()
        yaml.Unmarshal(configBuf, lineConfig)

        // Setup bot
        lineAdapter, _ := line.NewAdapter(lineConfig)
        storage := sarah.NewUserContextStorage(sarah.NewCacheConfig())
        lineBot, _ := sarah.NewBot(lineAdapter, sarah.BotWithStorage(storage))
	
        // Start
        rootCtx := context.Background()
        runnerCtx, _ := context.WithCancel(rootCtx)
        runner, _ := sarah.NewRunner(sarah.NewConfig(), sarah.WithBot(lineBot))
        runner.Run(runnerCtx)
}
```
