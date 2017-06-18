// Package counters provides a simple counter, max and min functionalities.
// All counters are kept in CounterBox.
// Library is thread safe.
package counters

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"text/template"
	"time"
)

// MaxMinValue is an interface for minima and maxima counters.
type MaxMinValue interface {
	// Set allows to update value if necessary.
	Set(int)
	// Name returns a name of counter.
	Name() string
	// Value returns a current value.
	Value() int64
}

// Counter is an interface for integer increase only counter.
type Counter interface {
	// Increment increases counter by one.
	Increment()
	// IncrementBy increases counter by given number.
	IncrementBy(num int)
	// Name returns a name of counter.
	Name() string
	// Value returns a current value of counter.
	Value() int64
}

// CounterBox is a main type, it keeps references to all counters
// requested from it.
type CounterBox struct {
	counters map[string]*counterImpl
	min      map[string]*minImpl
	max      map[string]*maxImpl
	m        *sync.RWMutex
}

// NewCounterBox creates a new object to keep all counters.
func NewCounterBox() *CounterBox {
	return &CounterBox{
		counters: make(map[string]*counterImpl),
		min:      make(map[string]*minImpl),
		max:      make(map[string]*maxImpl),
		m:        &sync.RWMutex{},
	}
}

// CreateHttpHandler creates a simple handler printing values of all counters.
func (c *CounterBox) CreateHttpHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.m.RLock()
		defer c.m.RUnlock()
		fmt.Fprintf(w, "Counters %d\n", len(c.counters))
		for k, v := range c.counters {
			fmt.Fprintf(w, "%s=%d\n", k, v.Value())
		}
		fmt.Fprintf(w, "\nMax values %d\n", len(c.max))
		for k, v := range c.max {
			fmt.Fprintf(w, "%s=%d\n", k, v.Value())
		}
		fmt.Fprintf(w, "\nMin values %d\n", len(c.min))
		for k, v := range c.min {
			fmt.Fprintf(w, "%s=%d\n", k, v.Value())
		}
	}
}

// GetCounter returns a counter of given name, if doesn't exist than create.
func (c *CounterBox) GetCounter(name string) Counter {
	c.m.RLock()
	v, ok := c.counters[name]
	c.m.RUnlock()
	if !ok {
		c.m.Lock()
		if v, ok = c.counters[name]; !ok {
			v = &counterImpl{name, 0}
			c.counters[name] = v
		}
		c.m.Unlock()
	}
	return v
}

// GetMin returns a minima counter of given name, if doesn't exist than create.
func (c *CounterBox) GetMin(name string) MaxMinValue {
	c.m.RLock()
	v, ok := c.min[name]
	c.m.RUnlock()
	if !ok {
		c.m.Lock()
		if v, ok = c.min[name]; !ok {
			v = &minImpl{name, math.MaxInt64}
			c.min[name] = v
		}
		c.m.Unlock()
	}
	return v
}

// GetMax returns a maxima counter of given name, if doesn't exist than create.
func (c *CounterBox) GetMax(name string) MaxMinValue {
	c.m.RLock()
	v, ok := c.max[name]
	c.m.RUnlock()
	if !ok {
		c.m.Lock()
		if v, ok = c.max[name]; !ok {
			v = &maxImpl{name, 0}
			c.max[name] = v
		}
		c.m.Unlock()
	}
	return v
}

var tmpl = template.Must(template.New("main").Parse(`== Counters ==
{{- range .Counters}}
  {{.Name}}: {{.Value}}
{{- end}}
== Min values ==
{{- range .Min}}
  {{.Name}}: {{.Value}}
{{- end}}
== Max values ==
{{- range .Max}}
  {{.Name}}: {{.Value}}
{{- end -}}
`))

func (c *CounterBox) WriteTo(w io.Writer) {
	c.m.RLock()
	defer c.m.RUnlock()
	data := &struct {
		Counters []Counter
		Min      []MaxMinValue
		Max      []MaxMinValue
	}{}
	for _, c := range c.counters {
		data.Counters = append(data.Counters, c)
	}
	for _, c := range c.min {
		data.Min = append(data.Min, c)
	}
	for _, c := range c.max {
		data.Max = append(data.Max, c)
	}
	sort.Slice(data.Counters, func(i, j int) bool { return strings.Compare(data.Counters[i].Name(), data.Counters[j].Name()) < 0 })
	sort.Slice(data.Min, func(i, j int) bool { return strings.Compare(data.Min[i].Name(), data.Min[j].Name()) < 0 })
	sort.Slice(data.Max, func(i, j int) bool { return strings.Compare(data.Max[i].Name(), data.Max[j].Name()) < 0 })
	tmpl.Execute(w, data)
}

func (c *CounterBox) String() string {
	buf := &bytes.Buffer{}
	c.WriteTo(buf)
	return buf.String()
}

type counterImpl struct {
	name  string
	value int64
}

func (c *counterImpl) Increment() {
	atomic.AddInt64(&c.value, 1)
}

func (c *counterImpl) IncrementBy(num int) {
	atomic.AddInt64(&c.value, int64(num))
}

func (c *counterImpl) Name() string {
	return c.name
}

func (c *counterImpl) Value() int64 {
	return atomic.LoadInt64(&c.value)
}

type maxImpl counterImpl

func (m *maxImpl) Set(v int) {
	done := false
	v64 := int64(v)
	for !done {
		if o := atomic.LoadInt64(&m.value); v64 > o {
			done = atomic.CompareAndSwapInt64(&m.value, o, v64)
		} else {
			done = true
		}
	}
}

func (m *maxImpl) Name() string {
	return m.name
}

func (m *maxImpl) Value() int64 {
	return atomic.LoadInt64(&m.value)
}

type minImpl counterImpl

func (m *minImpl) Set(v int) {
	done := false
	v64 := int64(v)
	for !done {
		if o := atomic.LoadInt64(&m.value); v64 < o {
			done = atomic.CompareAndSwapInt64(&m.value, o, v64)
		} else {
			done = true
		}
	}
}

func (m *minImpl) Name() string {
	return m.name
}

func (m *minImpl) Value() int64 {
	return atomic.LoadInt64(&m.value)
}

type TrivialLogger interface {
	Print(string)
}

func InitCountersOnSignal(logger TrivialLogger, box *CounterBox) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		lastInt := time.Now()
		for sig := range sigs {
			logger.Print(box.String())
			l := time.Now()
			if sig == syscall.SIGTERM || l.Sub(lastInt).Seconds() < 1. {
				os.Exit(0)
			}
			lastInt = l
		}
	}()
}

func LogCountersEvery(logger TrivialLogger, box *CounterBox, d time.Duration) {
	go func() {
		t := time.NewTicker(d)
		for range t.C {
			logger.Print(box.String())
		}
	}()
}
