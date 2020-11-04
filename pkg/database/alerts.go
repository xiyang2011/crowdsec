package database

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/crowdsecurity/crowdsec/pkg/database/ent"
	"github.com/crowdsecurity/crowdsec/pkg/database/ent/alert"
	"github.com/crowdsecurity/crowdsec/pkg/database/ent/decision"
	"github.com/crowdsecurity/crowdsec/pkg/database/ent/event"
	"github.com/crowdsecurity/crowdsec/pkg/database/ent/meta"
	"github.com/crowdsecurity/crowdsec/pkg/models"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

func (c *Client) CreateAlertBulk(machineId string, alertList []*models.Alert) ([]string, error) {
	var decisions []*ent.Decision
	var metas []*ent.Meta
	var events []*ent.Event

	ret := []string{}
	bulkSize := 20

	bulk := make([]*ent.AlertCreate, 0, bulkSize)
	for i, alertItem := range alertList {
		owner, err := c.QueryMachineByID(machineId)
		if err != nil {
			if errors.Cause(err) != UserNotExists {
				return []string{}, errors.Wrap(QueryFail, fmt.Sprintf("machine '%s': %s", alertItem.MachineID, err))
			}
			owner = nil
		}
		startAtTime, err := time.Parse(time.RFC3339, *alertItem.StartAt)
		if err != nil {
			return []string{}, errors.Wrap(ParseTimeFail, fmt.Sprintf("start_at field time '%s': %s", *alertItem.StartAt, err))
		}

		stopAtTime, err := time.Parse(time.RFC3339, *alertItem.StopAt)
		if err != nil {
			return []string{}, errors.Wrap(ParseTimeFail, fmt.Sprintf("stop_at field time '%s': %s", *alertItem.StopAt, err))
		}

		if len(alertItem.Events) > 0 {
			eventBulk := make([]*ent.EventCreate, len(alertItem.Events))
			for i, eventItem := range alertItem.Events {
				ts, err := time.Parse(time.RFC3339, *eventItem.Timestamp)
				if err != nil {
					return []string{}, errors.Wrap(ParseTimeFail, fmt.Sprintf("event timestamp '%s' : %s", *eventItem.Timestamp, err))
				}
				marshallMetas, err := json.Marshal(eventItem.Meta)
				if err != nil {
					return []string{}, errors.Wrap(MarshalFail, fmt.Sprintf("event meta '%v' : %s", eventItem.Meta, err))
				}

				eventBulk[i] = c.Ent.Event.Create().
					SetTime(ts).
					SetSerialized(string(marshallMetas))
			}
			events, err = c.Ent.Event.CreateBulk(eventBulk...).Save(c.CTX)
			if err != nil {
				return []string{}, errors.Wrap(BulkError, fmt.Sprintf("creating alert events: %s", err))
			}
		}

		if len(alertItem.Meta) > 0 {
			metaBulk := make([]*ent.MetaCreate, len(alertItem.Meta))
			for i, metaItem := range alertItem.Meta {
				metaBulk[i] = c.Ent.Meta.Create().
					SetKey(metaItem.Key).
					SetValue(metaItem.Value)
			}
			metas, err = c.Ent.Meta.CreateBulk(metaBulk...).Save(c.CTX)
			if err != nil {
				return []string{}, errors.Wrap(BulkError, fmt.Sprintf("creating alert meta: %s", err))
			}
		}

		if len(alertItem.Decisions) > 0 {
			decisionBulk := make([]*ent.DecisionCreate, len(alertItem.Decisions))
			for i, decisionItem := range alertItem.Decisions {
				duration, err := time.ParseDuration(*decisionItem.Duration)
				if err != nil {
					return []string{}, errors.Wrap(ParseDurationFail, fmt.Sprintf("decision duration '%v' : %s", decisionItem.Duration, err))
				}
				decisionBulk[i] = c.Ent.Decision.Create().
					SetUntil(time.Now().Add(duration)).
					SetScenario(*decisionItem.Scenario).
					SetType(*decisionItem.Type).
					SetStartIP(decisionItem.StartIP).
					SetEndIP(decisionItem.EndIP).
					SetValue(*decisionItem.Value).
					SetScope(*decisionItem.Scope).
					SetOrigin(*decisionItem.Origin).
					SetSimulated(*alertItem.Simulated)
			}
			decisions, err = c.Ent.Decision.CreateBulk(decisionBulk...).Save(c.CTX)
			if err != nil {
				return []string{}, errors.Wrap(BulkError, fmt.Sprintf("creating alert decisions: %s", err))

			}
		}

		alertB := c.Ent.Alert.
			Create().
			SetScenario(*alertItem.Scenario).
			SetMessage(*alertItem.Message).
			SetEventsCount(*alertItem.EventsCount).
			SetStartedAt(startAtTime).
			SetStoppedAt(stopAtTime).
			SetSourceScope(*alertItem.Source.Scope).
			SetSourceValue(*alertItem.Source.Value).
			SetSourceIp(alertItem.Source.IP).
			SetSourceRange(alertItem.Source.Range).
			SetSourceAsNumber(alertItem.Source.AsNumber).
			SetSourceAsName(alertItem.Source.AsName).
			SetSourceCountry(alertItem.Source.Cn).
			SetSourceLatitude(alertItem.Source.Latitude).
			SetSourceLongitude(alertItem.Source.Longitude).
			SetCapacity(*alertItem.Capacity).
			SetLeakSpeed(*alertItem.Leakspeed).
			SetSimulated(*alertItem.Simulated).
			SetScenarioVersion(*alertItem.ScenarioVersion).
			SetScenarioHash(*alertItem.ScenarioHash).
			AddDecisions(decisions...).
			AddEvents(events...).
			AddMetas(metas...)

		if owner != nil {
			alertB.SetOwner(owner)
		}
		bulk = append(bulk, alertB)

		if len(bulk) == bulkSize {
			alerts, err := c.Ent.Alert.CreateBulk(bulk...).Save(c.CTX)
			if err != nil {
				return []string{}, errors.Wrap(BulkError, fmt.Sprintf("creating alert : %s", err))
			}
			for _, alert := range alerts {
				ret = append(ret, strconv.Itoa(alert.ID))
			}

			if len(alertList)-i <= bulkSize {
				bulk = make([]*ent.AlertCreate, 0, (len(alertList) - i))
			} else {
				bulk = make([]*ent.AlertCreate, 0, bulkSize)
			}
		}
	}

	alerts, err := c.Ent.Alert.CreateBulk(bulk...).Save(c.CTX)
	if err != nil {
		return []string{}, errors.Wrap(BulkError, fmt.Sprintf("creating alert : %s", err))
	}

	for _, alert := range alerts {
		ret = append(ret, strconv.Itoa(alert.ID))
	}

	return ret, nil
}

func BuildAlertRequestFromFilter(alerts *ent.AlertQuery, filter map[string][]string) (*ent.AlertQuery, error) {
	var err error
	var startIP, endIP int64
	var hasActiveDecision bool

	/*the simulated filter is a bit different : if it's not present *or* set to false, specifically exclude records with simulated to true */
	if v, ok := filter["simulated"]; ok {
		if v[0] == "false" {
			alerts = alerts.Where(alert.SimulatedEQ(false))
		}
		delete(filter, "simulated")
	}

	for param, value := range filter {
		switch param {
		case "scope":
			alerts = alerts.Where(alert.SourceScopeEQ(value[0]))
		case "value":
			alerts = alerts.Where(alert.SourceValueEQ(value[0]))
		case "scenario":
			alerts = alerts.Where(alert.ScenarioEQ(value[0]))
		case "ip":
			isValidIP := IsIpv4(value[0])
			if !isValidIP {
				return nil, errors.Wrap(InvalidIPOrRange, fmt.Sprintf("unable to parse '%s': %s", value[0], err))
			}
			startIP, endIP, err = GetIpsFromIpRange(value[0] + "/32")
			if err != nil {
				return nil, errors.Wrap(InvalidIPOrRange, fmt.Sprintf("unable to convert '%s' to int interval: %s", value[0], err))
			}
		case "range":
			startIP, endIP, err = GetIpsFromIpRange(value[0])
			if err != nil {
				return nil, errors.Wrap(InvalidIPOrRange, fmt.Sprintf("unable to convert '%s' to int interval: %s", value[0], err))
			}
		case "since":
			duration, err := time.ParseDuration(value[0])
			if err != nil {
				return nil, errors.Wrap(err, "while parsing duration")
			}
			since := time.Now().Add(-duration)
			if since.IsZero() {
				return nil, fmt.Errorf("Empty time now() - %s", since.String())
			}
			alerts = alerts.Where(alert.CreatedAtGTE(since))
		case "until":
			duration, err := time.ParseDuration(value[0])
			if err != nil {
				return nil, errors.Wrap(err, "while parsing duration")
			}
			since := time.Now().Add(-duration)
			if since.IsZero() {
				return nil, fmt.Errorf("Empty time now() - %s", since.String())
			}
			alerts = alerts.Where(alert.CreatedAtLTE(since))
		case "decision_type":
			alerts = alerts.Where(alert.HasDecisionsWith(decision.TypeEQ(value[0])))
		case "include_capi": //allows to exclude one or more specific origins
			if value[0] == "false" {
				alerts = alerts.Where(alert.HasDecisionsWith(decision.OriginNEQ("CAPI")))
			} else if value[0] != "true" {
				log.Errorf("Invalid bool '%s' for include_capi", value[0])
			}
		case "has_active_decision":
			if hasActiveDecision, err = strconv.ParseBool(value[0]); err != nil {
				return nil, errors.Wrap(ParseType, fmt.Sprintf("'%s' is not a boolean: %s", value[0], err))
			}
			if hasActiveDecision {
				alerts = alerts.Where(alert.HasDecisionsWith(decision.UntilGTE(time.Now())))
			} else {
				alerts = alerts.Where(alert.Not(alert.HasDecisions()))
			}
		default:
			return nil, errors.Wrap(InvalidFilter, fmt.Sprintf("Filter parameter '%s' is unknown (=%s)", param, value[0]))
		}
	}
	if startIP != 0 && endIP != 0 {
		alerts = alerts.Where(alert.And(
			alert.HasDecisionsWith(decision.StartIPGTE(startIP)),
			alert.HasDecisionsWith(decision.EndIPLTE(endIP)),
		))
	}
	return alerts, nil
}

func (c *Client) QueryAlertWithFilter(filter map[string][]string) ([]*ent.Alert, error) {
	alerts := c.Ent.Alert.Query()
	alerts, err := BuildAlertRequestFromFilter(alerts, filter)
	if err != nil {
		return []*ent.Alert{}, err
	}
	alerts = alerts.
		WithDecisions().
		WithEvents().
		WithMetas().
		WithOwner()

	result, err := alerts.
		Order(ent.Asc(alert.FieldCreatedAt)).
		All(c.CTX)

	if err != nil {
		return []*ent.Alert{}, errors.Wrap(QueryFail, fmt.Sprintf("filter '%+v'", filter))
	}

	return result, nil
}

func (c *Client) DeleteAlertGraph(alertItem *ent.Alert) error {
	// delete the associated events
	_, err := c.Ent.Event.Delete().
		Where(event.HasOwnerWith(alert.IDEQ(alertItem.ID))).Exec(c.CTX)
	if err != nil {
		return errors.Wrapf(DeleteFail, "event with alert ID '%d'", alertItem.ID)
	}

	// delete the associated meta
	_, err = c.Ent.Meta.Delete().
		Where(meta.HasOwnerWith(alert.IDEQ(alertItem.ID))).Exec(c.CTX)
	if err != nil {
		return errors.Wrapf(DeleteFail, "meta with alert ID '%d'", alertItem.ID)
	}

	// delete the associated decisions
	_, err = c.Ent.Decision.Delete().
		Where(decision.HasOwnerWith(alert.IDEQ(alertItem.ID))).Exec(c.CTX)
	if err != nil {
		return errors.Wrapf(DeleteFail, "decision with alert ID '%d'", alertItem.ID)
	}

	// delete the alert
	err = c.Ent.Alert.DeleteOne(alertItem).Exec(c.CTX)
	if err != nil {
		return errors.Wrapf(DeleteFail, "alert with ID '%d'", alertItem.ID)
	}

	return nil
}

func (c *Client) DeleteAlertWithFilter(filter map[string][]string) ([]*ent.Alert, error) {
	var err error

	// Get all the alerts that match the filter
	alertsToDelete, err := c.QueryAlertWithFilter(filter)

	for _, alertItem := range alertsToDelete {
		err = c.DeleteAlertGraph(alertItem)
		if err != nil {
			return []*ent.Alert{}, errors.Wrap(DeleteFail, fmt.Sprintf("event with alert ID '%d'", alertItem.ID))
		}
	}
	return alertsToDelete, nil
}

func (c *Client) FlushAlerts(MaxAge time.Duration, MaxItems int) error {
	var totalDeleted int
	until := time.Now().Add(-MaxAge)

	if MaxAge > 0 {
		filter := map[string][]string{
			"until": {until.Format(time.RFC3339)},
		}
		deleted, err := c.DeleteAlertWithFilter(filter)
		if err != nil {
			return errors.Wrapf(err, "unable to flush alerts with filter %s", until.String())
		}
		totalDeleted += len(deleted)
	}
	if MaxItems > 0 {
		totalAlerts, err := c.Ent.Alert.Query().Count(c.CTX)
		if err != nil {
			return errors.Wrap(err, "unable to get alerts count")
		}
		if totalAlerts > MaxItems {
			nbToDelete := totalAlerts - MaxItems
			alerts, err := c.Ent.Alert.Query().
				WithDecisions().
				WithEvents().
				WithMetas().
				WithOwner().
				Order(ent.Asc(alert.FieldCreatedAt)).
				All(c.CTX)
			if err != nil {
				return errors.Wrap(err, "unable to get all alerts")
			}
			for itemNb, alert := range alerts {
				if itemNb < nbToDelete {
					err := c.DeleteAlertGraph(alert)
					if err != nil {
						return errors.Wrap(err, "unable to flush alert")
					}
				}
			}
			totalDeleted += nbToDelete
		}
	}
	log.Debugf("%d alerts automatically flushed", totalDeleted)

	return nil
}