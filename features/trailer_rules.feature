@bdd
Feature: Trailer rules from the documentation
  What the use cases and the aggregate canvas say about door work, holds, the unload policy and the dwell sweep, and
  that no other feature pins yet: the order of the door work, what a hold blocks, the version of a trailer, when closing
  a receipt completes the unload and when it does nothing, and the clock-driven dwell alert.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03,WH1-Y04" and doors "WH1-D01:Inbound,WH1-D02:Both,WH1-D03:Outbound"

  # ---- Door work -------------------------------------------------------------------------------------------------

  Scenario Outline: Door work needs a docked trailer and follows its order
    Given trailer "TR-1" is <situation>
    When the dock operator <action> on trailer "TR-1"
    Then the response status is 409
    And the problem is "illegal-transition"

    Examples:
      | situation                | action               |
      | parked                   | starts door work     |
      | parked                   | completes door work  |
      | docked at door "WH1-D02" | completes door work  |

  Scenario: A hold on a docked trailer is allowed and its door work waits for the release
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the supervisor places a QUALITY hold on trailer "TR-1"
    Then the response status is 200
    And trailer "TR-1" is on hold QUALITY
    And trailer "TR-1" is DOCKED at DOOR "WH1-D02"
    When the dock operator starts door work on trailer "TR-1"
    Then the response status is 409
    And the problem is "trailer-on-hold"
    And trailer "TR-1" has door work WAITING and load state LOADED
    When the supervisor releases the hold on trailer "TR-1"
    And the dock operator starts door work on trailer "TR-1"
    Then the response status is 200
    And trailer "TR-1" has door work IN_PROGRESS and load state LOADED

  Scenario: A trailer that is already on hold takes no second hold
    Given trailer "TR-1" is parked
    When the supervisor places a DAMAGED hold on trailer "TR-1"
    Then the response status is 200
    When the supervisor places a QUALITY hold on trailer "TR-1"
    Then the response status is 409
    And trailer "TR-1" is on hold DAMAGED
    And the event "TrailerHeld" was published 1 time

  Scenario: Releasing a hold that is not there changes nothing
    Given trailer "TR-1" is parked
    When the supervisor releases the hold on trailer "TR-1"
    Then the response status is 409
    And no "TrailerHoldReleased" event was published

  Scenario Outline: A departed trailer takes no hold, no seal and no move
    Given trailer "TR-1" has departed
    When I send POST "<path>" with <body>
    Then the response status is 409
    And the problem is "illegal-transition"

    Examples:
      | path                           | body                                                                   |
      | /trailers/<trailer TR-1>/holds | {"reason":"DAMAGED"}                                                   |
      | /trailers/<trailer TR-1>/seals | {"state":"INTACT","sealNumber":"SL-9"}                                 |
      | /moves                         | {"trailerId":"<trailer TR-1>","to":{"kind":"SPOT","code":"WH1-Y04"}} |

  Scenario: Each accepted change raises the version of a trailer by one and a refused change does not
    Given trailer "TR-1" is parked
    Then trailer "TR-1" is at version 2
    When the supervisor places a DAMAGED hold on trailer "TR-1"
    Then trailer "TR-1" is at version 3
    When the supervisor places a QUALITY hold on trailer "TR-1"
    Then the response status is 409
    And trailer "TR-1" is at version 3
    When I send GET "/trailers/<trailer TR-1>"
    Then the response ETag is version 3
    When the supervisor releases the hold on trailer "TR-1"
    Then trailer "TR-1" is at version 4

  Scenario: A live unload trailer stays at its door once the work is done
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And the gate clerk completes the move of trailer "TR-1"
    And the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    Then trailer "TR-1" has door work DONE and load state EMPTY
    And trailer "TR-1" is DOCKED at DOOR "WH1-D01"
    When I send GET "/moves?state=REQUESTED"
    Then the response list "items" has 0 items

  # ---- AutoCompleteUnload ----------------------------------------------------------------------------------------

  Scenario: Closing the receipt completes door work that was never started
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    And receipt "rcpt-1" is opened for ASN "ASN-1" on appointment "appt-1" at door "WH1-D01"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And the gate clerk completes the move of trailer "TR-1"
    Then trailer "TR-1" has door work WAITING and load state LOADED
    When inbound-receiving closes receipt "rcpt-1" for ASN "ASN-1"
    Then trailer "TR-1" has door work DONE and load state EMPTY
    And the event "DoorWorkCompleted" was published 1 time

  Scenario: The unload completes only when every ASN on the trailer has a closed receipt
    Given ASN "ASN-1" is registered
    And ASN "ASN-2" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASNs "ASN-1,ASN-2" starting in 10 minutes
    And receipt "rcpt-1" is opened for ASN "ASN-1" on appointment "appt-1" at door "WH1-D01"
    And receipt "rcpt-2" is opened for ASN "ASN-2" on appointment "appt-1" at door "WH1-D01"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And the gate clerk completes the move of trailer "TR-1"
    And the dock operator starts door work on trailer "TR-1"
    And inbound-receiving closes receipt "rcpt-1" for ASN "ASN-1"
    Then trailer "TR-1" has door work IN_PROGRESS and load state LOADED
    When inbound-receiving closes receipt "rcpt-2" for ASN "ASN-2"
    Then trailer "TR-1" has door work DONE and load state EMPTY

  Scenario: A receipt closed before the trailer is at the door completes nothing
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    And receipt "rcpt-1" is opened for ASN "ASN-1" on appointment "appt-1" at door "WH1-D01"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And inbound-receiving closes receipt "rcpt-1" for ASN "ASN-1"
    Then trailer "TR-1" is IN_MOVE
    And no "DoorWorkCompleted" event was published
    When the gate clerk completes the move of trailer "TR-1"
    Then trailer "TR-1" has door work WAITING and load state LOADED

  Scenario: Closing the receipt of work that is already done changes nothing
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    And receipt "rcpt-1" is opened for ASN "ASN-1" on appointment "appt-1" at door "WH1-D01"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And the gate clerk completes the move of trailer "TR-1"
    And the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1" leaving it PARTIAL
    And inbound-receiving closes receipt "rcpt-1" for ASN "ASN-1"
    Then trailer "TR-1" has door work DONE and load state PARTIAL
    And the event "DoorWorkCompleted" was published 1 time

  # ---- DwellSweep ------------------------------------------------------------------------------------------------

  Scenario: The dwell sweep raises one alert per trailer once its dwell reaches the threshold of its kind
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"dwellThresholdMinutes":{"DRY_VAN":60}}
    And trailer "TR-1" is parked
    When the clock advances 59 minutes
    And the dwell sweep runs
    Then the response field "count" is 0
    And no "TrailerDwellExceeded" event was published
    When the clock advances 2 minutes
    And the dwell sweep runs
    Then the response field "count" is 1
    And the event "TrailerDwellExceeded" was published 1 time
    And the latest "TrailerDwellExceeded" event carries "dwell_minutes" 61
    And the latest "TrailerDwellExceeded" event carries "threshold_minutes" 60
    And the latest "TrailerDwellExceeded" event carries "location_code" "WH1-Y01"
    When the dwell sweep runs
    Then the response field "count" is 0
    And the event "TrailerDwellExceeded" was published 1 time

  Scenario: Each trailer kind has its own threshold and a kind without one is never alerted
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"dwellThresholdMinutes":{"DRY_VAN":60,"REEFER":240}}
    And trailer "TR-1" is parked
    When the gate checks in visit "R" for purpose DROP with plate "REEF1" and trailer "RF-1" (REEFER, LOADED)
    And the gate clerk completes the move of trailer "RF-1"
    And the gate checks in visit "F" for purpose DROP with plate "FLAT1" and trailer "FB-1" (FLATBED, LOADED)
    And the gate clerk completes the move of trailer "FB-1"
    And the clock advances 120 minutes
    And the dwell sweep runs
    Then the response field "count" is 1
    And the latest "TrailerDwellExceeded" event carries "kind" "DRY_VAN"
    When the clock advances 200 minutes
    And the dwell sweep runs
    Then the response field "count" is 1
    And the latest "TrailerDwellExceeded" event carries "kind" "REEFER"
    When the clock advances 1000 minutes
    And the dwell sweep runs
    Then the response field "count" is 0
    And the event "TrailerDwellExceeded" was published 2 times

  Scenario: A trailer that has left the site is not alerted
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"dwellThresholdMinutes":{"DRY_VAN":60}}
    And trailer "TR-1" has departed
    When the clock advances 600 minutes
    And the dwell sweep runs
    Then the response field "count" is 0
    And no "TrailerDwellExceeded" event was published
