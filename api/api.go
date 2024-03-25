package api

import (
	"strings"
	"time"
)

type TransportConnection struct {
	addr  string
	nodes map[string]*TransportNode
}

type TransportNode struct {
	id                 string
	directions         map[string]string
	current_containers map[uint16]*Container
}

type TransportTask struct {
	uuid              string
	date_created      time.Time
	date_modified     time.Time
	status            string
	container_barcode string
	destination       string
}

type Container struct {
	barcode string
	task    *TransportTask
}

type LSTelegram struct {
	raw               string
	transport_node_id string
	telegram_type     string
	sequence_no       string
	addr1             string
	addr2             string
	barcode           string
	reserve           string
}

type Telegram struct {
	raw_input         string
	date_received     time.Time
	raw_response      string
	date_responded    time.Time
	response_builder  strings.Builder
	related_container *Container
}
