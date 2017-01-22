LINE adapter for [sarah](https://github.com/oklahomer/go-sarah).

```go
package main

import (
	"github.com/oklahomer/go-sarah"
	"github.com/oklahomer/go-sarah-line"
	"github.com/oklahomer/go-sarah/log"
	"golang.org/x/net/context"
    "gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"
)

func main() {
	rootCtx := context.Background()
	runnerCtx, cancelFunc := context.WithCancel(rootCtx)
	runner := sarah.NewRunner(sarah.NewConfig())

    // Setup bot
    configBuf, _ := ioutil.ReadFile("/path/to/adapter/config.yaml")
	lineConfig := line.NewConfig()
	yaml.Unmarshal(configBuf, lineConfig)
	lineAdapter := line.NewAdapter(lineConfig)
	lineBot := sarah.NewBot(lineAdapter, sarah.NewCacheConfig(), "")
	
	// Add command(s)
	echo := sarah.NewCommandBuilder().
	    Identifier("hello").
        MatchPattern(regexp.MustCompile(`^\.hello`)).
        Func(func(_ context.Context, input sarah.Input) (*sarah.CommandResponse, error) {
                return sarah.NewStringResponse("hello!!"), nil
        }).
        InputExample(".hello").
        MustBuild()
	lineBot.AppendCommand(echo)
	
	// Start
	runner.RegisterBot(lineBot)
	runner.Run(runnerCtx)

	func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		signal.Notify(c, syscall.SIGTERM)

		// Block til signal comes
		<-c

		log.Info("received signal")

		cancelFunc()
		time.Sleep(time.Duration(5) * time.Second)

		log.Info("stopped")
	}()
}
```
