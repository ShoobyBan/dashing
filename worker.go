package dashing

// A Job does periodic work and sends events to a channel.
type Job interface {
	Work(config *Config, send chan *Event)
}

// A Worker contains a collection of jobs.
type Worker struct {
	broker   *Broker
	config   *Config
	registry []Job
}

// Register a job for a particular worker.
func (w *Worker) Register(j Job) {
	if j == nil {
		panic("Can't register nil job")
	}
	w.registry = append(w.registry, j)
}

// Start all jobs.
func (w *Worker) Start() {
	for _, j := range w.registry {
		go j.Work(w.config, w.broker.events)
	}
}

// NewWorker returns a Worker instance.
func NewWorker(b *Broker, c *Config) *Worker {
	return &Worker{
		broker:   b,
		config:   c,
		registry: append([]Job(nil), jobs...),
	}
}

// Global registry for background jobs.
var jobs []Job

// Register a job to be kicked off upon starting a worker.
func Register(j Job) {
	if j == nil {
		panic("Can't register nil job")
	}
	jobs = append(jobs, j)
}
