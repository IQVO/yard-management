@bdd
Feature: What the floor client relies on
  The gate kiosk, the dock tablet and the hostler's phone call the same REST contract. These scenarios pin the details
  they were promised: optional trailers, the deviation note, the driver reference on a rejection, the filters, the
  absence of a lease renewal, optional keys on the sweeps and the not-found problems.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03" and doors "WH1-D01:Inbound,WH1-D02:Both"

  Scenario: A bobtail and an unscheduled pickup bring no trailer
    When the gate checks in visit "B" for purpose BOBTAIL with plate "BOB1"
    Then the response status is 201
    And the response list "trailers" has 0 items
    And the response list "moves" has 0 items
    When the gate checks in visit "P" for purpose PICKUP with plate "PU1"
    Then the response status is 201

  Scenario: A drop brings a trailer
    When the gate checks in visit "D" for purpose DROP with plate "DR1"
    Then the response status is 422
    And the problem is "validation"

  Scenario: A pickup that names an appointment brings exactly one trailer
    Given appointment "appt-9" is booked at door "WH1-D02" for shipment "SHP-9" starting in 10 minutes
    When the gate checks in visit "P" for purpose PICKUP with plate "PU2" on appointment "appt-9" and no trailer
    Then the response status is 422
    And the problem is "unknown-appointment"

  Scenario: The deviation note explains a completion elsewhere
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    And hostler "h1" claims the next move at site "WH1"
    And hostler "h1" completes the move of trailer "TR-1" at SPOT "WH1-Y02"
    Then the response status is 422
    And the problem is "unexplained-destination"
    When hostler "h1" completes the move of trailer "TR-1" at SPOT "WH1-Y02" noting "Y03 was blocked by a pallet"
    Then the response status is 200
    And the response field "actualLocation.code" is "WH1-Y02"
    And trailer "TR-1" is PARKED at SPOT "WH1-Y02"

  Scenario: A hostler lists the moves it holds and a visit lists its trailers
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    And hostler "h1" claims the next move at site "WH1"
    And I send GET "/moves?hostlerRef=h1"
    Then the response list "items" has 1 item
    And the response field "items.0.hostlerRef" is "h1"
    When I send GET "/moves?hostlerRef=h2"
    Then the response list "items" has 0 items
    When I send GET "/trailers?visitId=<visit TR-2>"
    Then the response list "items" has 1 item
    And the response field "items.0.unitNumber" is "TR-2"
    When I send GET "/trailers?visitId=visit-nope"
    Then the response list "items" has 0 items

  Scenario: There is no lease renewal
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    And hostler "h1" claims the next move at site "WH1"
    And I send POST "/moves/<move TR-1>/lease-renewals"
    Then the response status is 404
    And the problem is "resource-not-found"

  Scenario: The sweeps take no Idempotency-Key
    When I send POST "/dwell-sweeps" without an Idempotency-Key
    Then the response status is 200
    And the response field "count" is 0
    When I send POST "/move-lease-sweeps" without an Idempotency-Key
    Then the response status is 200

  Scenario Outline: A missing resource names itself in the problem
    Given trailer "TR-1" is parked
    When I send <method> "<path>"
    Then the response status is 404
    And the problem is "<slug>"

    Examples:
      | method | path                                | slug                 |
      | GET    | /visits/visit-nope                  | visit-not-found      |
      | GET    | /trailers/trl-nope                  | trailer-not-found    |
      | POST   | /moves/mv-nope/completions          | move-not-found       |
      | POST   | /moves/mv-nope/cancellations        | move-not-found       |
      | POST   | /yard-checks/yck-nope/closures      | yard-check-not-found |
      | GET    | /sites/NOWHERE/yard-board           | site-not-found       |

  Scenario: A repeated request with the same Idempotency-Key is answered once
    When I send POST "/yard-checks" with the Idempotency-Key "key-1" and body {"siteId":"WH1"}
    And I send POST "/yard-checks" with the Idempotency-Key "key-1" and body {"siteId":"WH1"}
    Then the response status is 201
    And the response header "Idempotency-Replayed" is "true"
    When I send POST "/yard-checks" with the Idempotency-Key "key-1" and body {"siteId":"WH2"}
    Then the response status is 409
    And the problem is "idempotency-key-reused"
    When I send POST "/yard-checks" without an Idempotency-Key
    Then the response status is 400
    And the problem is "idempotency-key-required"

  Scenario: A yard check reports what the model puts where nobody looked
    Given trailer "TR-1" is parked
    When I send POST "/yard-checks" with {"siteId":"WH1"}
    And I send POST "/yard-checks/yck-000001/observations" with {"locationCode":"WH1-Y02"}
    And I send POST "/yard-checks/yck-000001/closures"
    Then the response status is 200
    And the response field "state" is "CLOSED"
    And the response field "discrepancies.0.kind" is "MISSING"
    And the response field "discrepancies.0.locationCode" is "WH1-Y01"
    And the event "YardCheckClosed" was published

  Scenario: The yard policy is versioned by its ETag
    When I send GET "/sites/WH1/yard-policy"
    Then the response ETag is version 1
    And the response field "declared" is false
    When I send PUT "/sites/WH1/yard-policy" with If-Match version 1 and body {"requireAppointment":true,"sealMismatchAction":"REJECT","leaseSeconds":120}
    Then the response status is 200
    And the response ETag is version 2
    When I send PUT "/sites/WH1/yard-policy" with If-Match version 1 and body {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300}
    Then the response status is 412
    And the problem is "precondition-failed"

  Scenario: The reports keep their shape before analytics exists
    When I send GET "/reports/yard-dwell?from=2026-11-01T00:00:00Z&to=2026-11-02T00:00:00Z"
    Then the response status is 200
    And the response list "rows" has 0 items
    And the response field "siteId" is null
    When I send GET "/reports/yard-dwell?from=2026-11-02T00:00:00Z&to=2026-11-01T00:00:00Z"
    Then the response status is 400
    And the problem is "invalid-report-range"
