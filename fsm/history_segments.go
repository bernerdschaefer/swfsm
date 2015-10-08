package fsm

import (
	"fmt"

	"strings"

	"encoding/json"
	"reflect"

	"strconv"

	"time"

	"github.com/aws/aws-sdk-go/service/swf"
	. "github.com/sclasen/swfsm/sugar"
)

type HistorySegment struct {
	State                   *HistorySegmentState
	Correlator              *EventCorrelator
	Events                  []*HistorySegmentEvent
	WorkflowId              *string
	ContinuedExecutionRunId *string
}

type HistorySegmentState struct {
	ID        *int64
	Timestamp *time.Time
	Version   *uint64
	Name      *string
	Data      *interface{}
}

type HistorySegmentEvent struct {
	ID         *int64
	Timestamp  *time.Time
	Type       *string
	Attributes *map[string]interface{}
	References []*int64
}

type historySegmentor struct {
	c *client
}

func newHistorySegmentor(c *client) *historySegmentor {
	return &historySegmentor{
		c: c,
	}
}

func (s *historySegmentor) FromHistoryEventIterator(itr HistoryEventIterator) ([]HistorySegment, error) {
	snapshots := []HistorySegment{}
	var err error

	zero := s.c.f.zeroStateData()
	unrecordedName := "<unrecorded>"
	unrecordedId := int64(999999)
	unrecordedVersion := uint64(999999)

	refs := make(map[int64][]*int64)
	snapshot := HistorySegment{Events: []*HistorySegmentEvent{}}
	var nextCorrelator *EventCorrelator
	event, err := itr()
	for ; event != nil; event, err = itr() {
		if err != nil {
			return snapshots, err
		}

		if s.c.f.isCorrelatorMarker(event) {
			correlator, err := s.c.f.findSerializedEventCorrelator([]*swf.HistoryEvent{event})
			if err != nil {
				break
			}
			nextCorrelator = correlator
			continue
		}

		state, err := s.c.f.statefulHistoryEventToSerializedState(event)
		if err != nil {
			break
		}

		if state != nil {
			if snapshot.State != nil {
				snapshots = append(snapshots, snapshot)
				snapshot = HistorySegment{Events: []*HistorySegmentEvent{}}
			}

			snapshot.State = &HistorySegmentState{
				ID:        event.EventId,
				Timestamp: event.EventTimestamp,
				Version:   &state.StateVersion,
				Name:      S(state.StateName),
				Data:      &zero,
			}
			err = s.c.f.Serializer.Deserialize(state.StateData, snapshot.State.Data)
			if err != nil {
				break
			}

			snapshot.WorkflowId = &state.WorkflowId

			if event.WorkflowExecutionStartedEventAttributes != nil {
				snapshot.ContinuedExecutionRunId = event.WorkflowExecutionStartedEventAttributes.ContinuedExecutionRunId
			}

			snapshot.Correlator = nextCorrelator
			nextCorrelator = nil

			continue
		}

		if snapshot.State == nil {
			snapshot.State = &HistorySegmentState{
				Name:    &unrecordedName,
				ID:      &(unrecordedId),
				Version: &unrecordedVersion,
			}
		}

		eventAttributes, err := s.snapshotEventAttributesMap(event)
		if err != nil {
			break
		}

		for key, value := range eventAttributes {
			if strings.HasSuffix(key, "EventId") {
				parsed, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
				if err != nil {
					break
				}
				refs[parsed] = append(refs[parsed], event.EventId)
			}
		}

		snapshot.Events = append(snapshot.Events, &HistorySegmentEvent{
			Type:       event.EventType,
			ID:         event.EventId,
			Timestamp:  event.EventTimestamp,
			Attributes: &eventAttributes,
			References: refs[*event.EventId],
		})
	}

	if snapshot.State != nil {
		snapshots = append(snapshots, snapshot)
	}

	return snapshots, err
}

func (s *historySegmentor) snapshotEventAttributesMap(e *swf.HistoryEvent) (map[string]interface{}, error) {
	attrStruct := reflect.ValueOf(*e).FieldByName(*e.EventType + "EventAttributes").Interface()
	attrJsonBytes, err := json.Marshal(attrStruct)
	if err != nil {
		return nil, err
	}

	attrMap := make(map[string]interface{})
	err = json.Unmarshal(attrJsonBytes, &attrMap)
	if err != nil {
		return nil, err
	}

	for k, v := range attrMap {
		tryValueMap := make(map[string]interface{})
		tryErr := json.Unmarshal([]byte(fmt.Sprint(v)), &tryValueMap)
		if tryErr == nil {
			attrMap[k] = tryValueMap
		}
	}
	return attrMap, nil
}