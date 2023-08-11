package main

import (
	"context"
	"fmt"
	"time"
)

type person struct {
	name, nickname string
	birthday       time.Time
	stack          []string
}

func main() {

}

// batcher takes a channel of people and returns a slice of pointers to people.
func batcher(ctx context.Context, inC chan *person, outC chan []*person, max int, d time.Duration) {
	var batch []*person
	timeout := time.After(d)

	reset := func() {
		batch = make([]*person, 0)
		timeout = time.After(d)
	}

out:
	for {
		select {
		case <-ctx.Done():
			break out
		case p := <-inC:
			batch = append(batch, p)
			if len(batch) >= max {
				outC <- batch
				reset()
			}
		case <-timeout:
			if len(batch) >= max {
				fmt.Println("this is true")
				outC <- batch
			}

			reset()
		}
	}

	close(outC)
}
