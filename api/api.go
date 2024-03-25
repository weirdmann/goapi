package api

import (
	"strings"
	"time"
)

/*

	telegram received
	parse

	SCN - Scan:
		respond with SCNA
		find container and its task
		respond with appropriate direction // direction might be based on some logic other than node directions
			chain of responsibility with an appropriate priority
			a link in the chain might decide the final direction and break the
			execution with an "early return"
		notify any observers
			observer pattern:
				- task status update
				- container spotted info
				- eventual error information

	SOK - Transport confirmed:
		respond with SOKA
		find container and its task
		notify any observers:
			- task status update
			- error if container unknown
	SER - Transport error
		respond with SERA
		find container and its task
		notify any observers:
			- log error cause (2 - location busy, 3 - CMD timeout)
			- TODO - update location status
	CMDA - Command received
		verbose log
*/

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
	container         *Container
	destination       string
}

type Container struct {
	barcode string
	task    *TransportTask
}

type LSTelegram struct {
	timestamp         time.Time
	raw               string
	transport_node_id string
	telegram_type     string
	sequence_no       string
	addr1             string
	addr2             string
	barcode           string
	reserve           string
}

type Pipeline struct {
	node      *TransportNode
	container *Container
	response  *LSTelegram
}

type Telegram struct {
	raw_input         string
	date_received     time.Time
	raw_response      string
	date_responded    time.Time
	response_builder  strings.Builder
	related_container *Container
}

type TelegramHandler interface {
	exec(*Pipeline)
	set_next(*TelegramHandler)
}
