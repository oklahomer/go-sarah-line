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
)

func main() {
        rootCtx := context.Background()
        runnerCtx, cancelRunner := context.WithCancel(rootCtx)
        runner := sarah.NewRunner(sarah.NewConfig())

        // Setup bot
        configBuf, _ := ioutil.ReadFile("/path/to/adapter/config.yaml")
        lineConfig := line.NewConfig()
        yaml.Unmarshal(configBuf, lineConfig)
        lineAdapter := line.NewAdapter(lineConfig)
        lineBot := sarah.NewBot(lineAdapter, sarah.NewCacheConfig())
	
        // Add command(s)
        echo := sarah.NewCommandBuilder().
                Identifier("hello").
                MatchPattern(regexp.MustCompile(`^\.hello`)).
                Func(func(_ context.Context, input sarah.Input) (*sarah.CommandResponse, error) {
                        return line.NewStringResponse("hello!!"), nil
                }).
                InputExample(".hello").
                MustBuild()
        lineBot.AppendCommand(echo)
	
        // Start
        runner.RegisterBot(lineBot)
        runner.Run(runnerCtx)
        runnerStop := make(chan struct{})
        go func() {
                runner.Run(runnerCtx)
                runnerStop <- struct{}{}
        }()

        c := make(chan os.Signal, 1)
        signal.Notify(c, os.Interrupt)
        signal.Notify(c, syscall.SIGTERM)

        select {
        case <-c:
		        log.Info("Canceled Runner.")
		        cancelRunner()
        case <-runnerStop:
                log.Error("Runner stopped.")
                // Stop because all bots stopped
	    }
}
```
