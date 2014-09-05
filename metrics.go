package kubismus

import (
	"sort"
	"time"
)

type Op int

const (
	COUNT Op = 1 << iota
	AVERAGE
	SUM
)

const (
	cMETRICS = 960 // number of metrics kept
)

type metric struct {
	name  string
	count int32
	value float64
	cData []float64
	vData []float64
}

type getmetric struct {
	name  string
	mtype Op
	reply chan []float64
}

type metricDef struct {
	Name        string
	Op          string
	DisplayName string
}

// sortMetricDef defines how to sort a slice of metricDefs
type sortMetricDef []metricDef

func (a sortMetricDef) Len() int      { return len(a) }
func (a sortMetricDef) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a sortMetricDef) Less(i, j int) bool {
	return a[i].Name < a[j].Name || (a[i].Name == a[j].Name && a[i].Op < a[j].Op)
}

var (
	metricChan     = make(chan metric, 1024)
	getMetricsChan = make(chan getmetric, 16)
	freeListM      = make(chan []float64, 64)
	getNamesChan   = make(chan chan []metricDef)
)

// init sets up the metrics system
func init() {
	go metricService()
}

// getMetricDefs returns the metric definitions
func getMetricDefs() []metricDef {
	c := make(chan []metricDef)
	getNamesChan <- c
	return <-c
}

// getMetrics returns a list of values for a metric
func getMetrics(name string, mtype Op) []float64 {
	c := getmetric{name: name, mtype: mtype, reply: make(chan []float64)}
	getMetricsChan <- c
	return <-c.reply
}

// releaseMetrics returns the slice of values to the leaky buffer, if possible.
// While not required, using it reduces work for the garbage collector.
func releaseMetrics(m []float64) {
	// Reuse buffer if there's room.
	select {
	case freeListM <- m:
		// Buffer on free list; nothing more to do.
	default:
		// Free list full, just carry on.
	}
}

// shift moves the slice values left, allowing room for a new value
func shift(a []float64) {
	for i := 0; i < len(a)-1; i++ {
		a[i] = a[i+1]
	}
}

// Define defines a metric with a given operation and display name.
func Define(reading string, mtype Op, DisplayName string) {

}

// Metric records a count and total value for a given reading. count should be 1 unless you are providing
// summed data for multiple events as the value. For instance, you can send the total bytes read for 3 files
// at one time.
func Metric(reading string, count int32, value float64) {
	metricChan <- metric{name: reading, count: count, value: value}
}

// metricService handles metrics processing
func metricService() {
	metrics := make(map[string]*metric)
	tck := time.NewTicker(1 * time.Second)
	for {
		select {
		case m := <-metricChan:
			if m.name != "" {
				v, ok := metrics[m.name]
				if !ok {
					v = &metric{name: m.name, count: 0, value: 0.0, cData: make([]float64, cMETRICS), vData: make([]float64, cMETRICS)}
					metrics[m.name] = v
				}
				v.count += m.count
				v.value += m.value
			}
		case <-tck.C:
			for _, y := range metrics {
				shift(y.cData)
				y.cData[len(y.cData)-1] = float64(y.count)
				shift(y.vData)
				y.vData[len(y.vData)-1] = y.value
				y.count = 0
				y.value = 0.0
			}
		case gm := <-getMetricsChan:
			var r []float64
			// Grab a buffer if available; allocate if not.
			select {
			case r = <-freeListM:
				// Got one; nothing more to do but slice it.
			default:
				// None free, so allocate a new one.
				r = make([]float64, cMETRICS)
			}
			v, ok := metrics[gm.name]
			if !ok {
				gm.reply <- nil
			} else {
				r = r[0:len(v.cData)]
				switch gm.mtype {
				case COUNT:
					copy(r, v.cData)
				case AVERAGE:
					for i, c := range v.cData {
						if c == 0.0 || c < 0.000000001 {
							r[i] = v.vData[i]
						} else {
							r[i] = v.vData[i] / c
						}
					}
				case SUM:
					copy(r, v.vData)
				}
				gm.reply <- r
			}
		case gn := <-getNamesChan:
			s := make([]metricDef, 0, 8)
			for x, _ := range metrics {
				s = append(s, metricDef{Name: x, Op: "count", DisplayName: x + " - count"})
				s = append(s, metricDef{Name: x, Op: "average", DisplayName: x + " - average"})
				s = append(s, metricDef{Name: x, Op: "sum", DisplayName: x + "- sum"})
			}
			sort.Sort(sortMetricDef(s))
			gn <- s
		}
	}
}
