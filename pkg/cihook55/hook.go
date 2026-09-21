package cihook55

import "sync"

type Pause func()

var (
	mu     sync.Mutex
	pauses = map[string]Pause{}
)

func Set(name string, p Pause) {
	mu.Lock()
	defer mu.Unlock()
	if p == nil {
		delete(pauses, name)
		return
	}
	pauses[name] = p
}

func Fire(name string) {
	mu.Lock()
	p := pauses[name]
	mu.Unlock()
	if p != nil {
		p()
	}
}
