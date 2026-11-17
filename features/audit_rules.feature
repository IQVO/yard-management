@bdd
Feature: Yard policy, yard check and yard board rules from the documentation
  What the use cases and the aggregate canvas say about DeclareYardPolicy, the yard check and the yard board and that no
  other feature pins yet: the defaults, the validation of a declaration, one open check per site, the three kinds of
  discrepancy, and the live map as a read model of the trailers and moves.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03,WH1-Y04" and doors "WH1-D01:Inbound,WH1-D02:Both,WH1-D03:Outbound"

  # ---- YardPolicy ------------------------------------------------------------------------------------------------

  Scenario: A site that never declared a policy answers the defaults
    When I send GET "/sites/WH1/yard-policy"
    Then the response status is 200
    And the response field "declared" is false
    And the response field "version" is 1
    And the response field "requireAppointment" is false
    And the response field "sealMismatchAction" is "FLAG"
    And the response field "leaseSeconds" is 300
    And the response field "freeTimeMinutes" is null
    And the response list "holdingSpotCodes" has 0 items
    And the response list "parkingZoneIds" has 0 items

  Scenario Outline: A policy with a non-positive lease, threshold or free time, or an unknown action, is refused
    When I send PUT "/sites/WH1/yard-policy" with <body>
    Then the response status is 422
    And the problem is "validation"

    Examples:
      | body                                                                                                                  |
      | {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":0}                                             |
      | {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":-5}                                            |
      | {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"dwellThresholdMinutes":{"DRY_VAN":0}}   |
      | {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"dwellThresholdMinutes":{"TRUCK":30}}    |
      | {"requireAppointment":false,"sealMismatchAction":"IGNORE","leaseSeconds":300}                                         |
      | {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"freeTimeMinutes":-1}                     |

  Scenario: The smallest positive lease and threshold are accepted
    When I send PUT "/sites/WH1/yard-policy" with {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":1,"dwellThresholdMinutes":{"DRY_VAN":1},"freeTimeMinutes":0}
    Then the response status is 200
    And the response field "leaseSeconds" is 1
    And the response field "dwellThresholdMinutes.DRY_VAN" is 1

  Scenario: Declaring the policy again replaces it as a whole
    Given the yard policy of site "WH1" is {"requireAppointment":true,"sealMismatchAction":"REJECT","leaseSeconds":120,"holdingSpotCodes":["WH1-Y03"],"dwellThresholdMinutes":{"DRY_VAN":90}}
    When I send PUT "/sites/WH1/yard-policy" with {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300}
    Then the response status is 200
    And the response field "version" is 3
    And the response field "requireAppointment" is false
    And the response field "sealMismatchAction" is "FLAG"
    And the response list "holdingSpotCodes" has 0 items
    And the response field "dwellThresholdMinutes" is {}

  # ---- YardCheck -------------------------------------------------------------------------------------------------

  Scenario: A site has at most one open yard check
    When I send POST "/yard-checks" with {"siteId":"WH1"}
    Then the response status is 201
    And the response field "state" is "OPEN"
    When I send POST "/yard-checks" with {"siteId":"WH1"}
    Then the response status is 409
    And the problem is "yard-check-open"
    When I send POST "/yard-checks" with {"siteId":"WH2"}
    Then the response status is 201
    When I send POST "/yard-checks/yck-000001/closures"
    And I send POST "/yard-checks" with {"siteId":"WH1"}
    Then the response status is 201

  Scenario: A location is observed once per check and a closed check takes no more observations
    When I send POST "/yard-checks" with {"siteId":"WH1"}
    And I send POST "/yard-checks/yck-000001/observations" with {"locationCode":"WH1-Y02"}
    Then the response status is 200
    When I send POST "/yard-checks/yck-000001/observations" with {"locationCode":"WH1-Y02","unitNumber":"TR-4"}
    Then the response status is 409
    And the problem is "location-already-observed"
    When I send POST "/yard-checks/yck-000001/observations" with {"locationCode":""}
    Then the response status is 422
    And the problem is "validation"
    When I send POST "/yard-checks/yck-000001/closures"
    Then the response status is 200
    When I send POST "/yard-checks/yck-000001/observations" with {"locationCode":"WH1-Y03"}
    Then the response status is 409
    And the problem is "illegal-transition"
    When I send POST "/yard-checks/yck-000001/closures"
    Then the response status is 409
    And the problem is "illegal-transition"
    And the event "YardCheckClosed" was published 1 time

  Scenario: A yard check tells an unknown unit, a unit in the wrong place and the trailer it replaced apart
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When I send POST "/yard-checks" with {"siteId":"WH1"}
    And I send POST "/yard-checks/yck-000001/observations" with {"locationCode":"WH1-Y01","unitNumber":"TR-2"}
    And I send POST "/yard-checks/yck-000001/observations" with {"locationCode":"WH1-Y03","unitNumber":"TR-9"}
    And I send POST "/yard-checks/yck-000001/closures"
    Then the response status is 200
    And the response list "discrepancies" has 3 items
    And the response field "discrepancies.0.kind" is "WRONG_LOCATION"
    And the response field "discrepancies.0.locationCode" is "WH1-Y01"
    And the response field "discrepancies.0.unitNumber" is "TR-2"
    And the response field "discrepancies.0.trailerId" is "trl-000002"
    And the response field "discrepancies.1.kind" is "UNEXPECTED"
    And the response field "discrepancies.1.locationCode" is "WH1-Y03"
    And the response field "discrepancies.1.unitNumber" is "TR-9"
    And the response field "discrepancies.1.trailerId" is null
    And the response field "discrepancies.2.kind" is "MISSING"
    And the response field "discrepancies.2.locationCode" is "WH1-Y01"
    And the response field "discrepancies.2.unitNumber" is "TR-1"

  Scenario: A spot observed empty where the model records a trailer is missing and closing leaves the model alone
    Given trailer "TR-1" is parked
    When I send POST "/yard-checks" with {"siteId":"WH1"}
    And I send POST "/yard-checks/yck-000001/observations" with {"locationCode":"WH1-Y01"}
    And I send POST "/yard-checks/yck-000001/closures"
    Then the response list "discrepancies" has 1 item
    And the response field "discrepancies.0.kind" is "MISSING"
    And the response field "discrepancies.0.locationCode" is "WH1-Y01"
    And the response field "discrepancies.0.unitNumber" is "TR-1"
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"

  Scenario: A check finds nothing to report where the trailers are where the model puts them
    Given trailer "TR-1" is parked
    When I send POST "/yard-checks" with {"siteId":"WH1"}
    And I send POST "/yard-checks/yck-000001/observations" with {"locationCode":"WH1-Y01","unitNumber":"TR-1"}
    And I send POST "/yard-checks/yck-000001/observations" with {"locationCode":"WH1-Y02"}
    And I send POST "/yard-checks/yck-000001/closures"
    Then the response list "discrepancies" has 0 items
    And the event "YardCheckClosed" was published 1 time

  Scenario: Only trailers that have a place on the site of the check are expected
    Given a yard site "WH2" with spots "WH2-Y01" and doors "WH2-D01:Both"
    And the gate works at site "WH1"
    When the gate checks in visit "A" for purpose DROP with plate "AAA1" and trailer "TR-1" (DRY_VAN, LOADED)
    And I send POST "/yard-checks" with {"siteId":"WH1"}
    And I send POST "/yard-checks/yck-000001/closures"
    Then the response list "discrepancies" has 0 items
    When the gate clerk completes the move of trailer "TR-1"
    And I send POST "/yard-checks" with {"siteId":"WH2"}
    And I send POST "/yard-checks/yck-000002/closures"
    Then the response list "discrepancies" has 0 items

  # ---- YardBoardView ---------------------------------------------------------------------------------------------

  Scenario: The yard board follows a trailer from the gate to its door and out
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    Then the yard board of site "WH1" lists 1 trailer in move and 1 open move
    And the yard board of site "WH1" shows door "WH1-D01" free
    When the gate clerk completes the move of trailer "TR-1"
    Then the yard board of site "WH1" lists 0 trailers in move and 0 open moves
    And the yard board of site "WH1" shows trailer "TR-1" at door "WH1-D01"
    When the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    And visit "A" checks out taking trailers "TR-1"
    Then the yard board of site "WH1" shows door "WH1-D01" free

  Scenario: The yard board shows a parked trailer on its spot, a claimed one in motion and a door under closure
    Given trailer "TR-1" is parked
    And the door "WH1-D02" is closed for the next 2 hours
    Then the yard board of site "WH1" shows trailer "TR-1" at spot "WH1-Y01"
    And the yard board of site "WH1" shows door "WH1-D02" closed
    And the yard board of site "WH1" shows door "WH1-D01" open
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1"
    Then the yard board of site "WH1" shows spot "WH1-Y01" free
    And the yard board of site "WH1" lists 1 trailer in move and 1 open move
    When the closure of door "WH1-D02" is lifted
    Then the yard board of site "WH1" shows door "WH1-D02" open
