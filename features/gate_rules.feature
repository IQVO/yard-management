@bdd
Feature: Gate rules from the documentation
  What the use cases, the aggregate canvas and ADR 0002 say about CheckInVisit, DirectOnCheckIn and CheckOutVisit
  and that no other feature pins yet: carrier and plate invariants, the appointment copy, where the first move goes,
  and what a check-out takes.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03,WH1-Y04" and doors "WH1-D01:Inbound,WH1-D02:Both,WH1-D03:Outbound"

  # ---- CheckInVisit: the carrier and the plate -------------------------------------------------------------------

  Scenario Outline: A carrier name is 1 to 100 characters
    When the gate checks in a bobtail with plate "BOB-1" for a carrier named with <length> characters
    Then the response status is <status>

    Examples:
      | length | status |
      | 100    | 201    |
      | 101    | 422    |
      | 0      | 422    |

  Scenario Outline: A carrier SCAC of two to four upper-case letters is accepted
    When the gate checks in a bobtail with plate "BOB-2" for carrier "Acme Freight" with SCAC "<scac>"
    Then the response status is 201
    And the response field "visit.carrierScac" is "<scac>"

    Examples:
      | scac |
      | AB   |
      | ABCD |

  Scenario Outline: A carrier SCAC that is not two to four upper-case letters is an invalid carrier
    When the gate checks in a bobtail with plate "BOB-3" for carrier "Acme Freight" with SCAC "<scac>"
    Then the response status is 422
    And the problem is "invalid-carrier"
    And no "VisitCheckedIn" event was published

    Examples:
      | scac  |
      | A     |
      | ABCDE |
      | acme  |
      | AC1D  |

  Scenario: One open visit per tractor plate is a rule of the site and of the visit that is still checked in
    Given a yard site "WH2" with spots "WH2-Y01,WH2-Y02" and doors "WH2-D01:Both"
    And the gate works at site "WH1"
    When the gate rejects a visit with plate "SAO4F77" because SITE_REQUEST
    And the gate checks in visit "A" for purpose BOBTAIL with plate "SAO4F77"
    Then the response status is 201
    When I send POST "/visits" with {"siteId":"WH2","carrier":"Acme Freight","tractorPlate":"SAO4F77","purpose":"BOBTAIL"}
    Then the response status is 201
    When the gate checks in visit "B" for purpose BOBTAIL with plate "SAO4F77"
    Then the response status is 409
    And the problem is "visit-already-open"
    When visit "A" checks out taking no trailers
    And the gate checks in visit "C" for purpose BOBTAIL with plate "SAO4F77"
    Then the response status is 201

  Scenario: A check-in replayed with the same Idempotency-Key is answered once
    When I send POST "/visits" with the Idempotency-Key "ci-1" and body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"SAO4F77","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED"}]}
    And I send POST "/visits" with the Idempotency-Key "ci-1" and body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"SAO4F77","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED"}]}
    Then the response status is 201
    And the response header "Idempotency-Replayed" is "true"
    And the event "VisitCheckedIn" was published 1 time
    When I send GET "/trailers"
    Then the response list "items" has 1 item

  Scenario: The natural key of a trailer on site is its carrier and its number
    Given trailer "TR-1" is parked
    When I send POST "/visits" with {"siteId":"WH1","carrier":"Beta Haulage","carrierScac":"BETA","tractorPlate":"BETA-1","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"EMPTY"}]}
    Then the response status is 201
    When I send GET "/trailers"
    Then the response list "items" has 2 items
    When the gate checks in visit "C" for purpose DROP with plate "ACME-2" and trailer "TR-1" (DRY_VAN, LOADED)
    Then the response status is 409
    And the problem is "trailer-already-on-site"

  Scenario: A trailer that has left the site may come back
    Given trailer "TR-1" has departed
    When the gate checks in visit "B" for purpose DROP with plate "RETURN1" and trailer "TR-1" (DRY_VAN, LOADED)
    Then the response status is 201

  # ---- CheckInVisit: the appointment -----------------------------------------------------------------------------

  Scenario: A site that requires appointments admits a visit with a booked appointment
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    And the yard policy of site "WH1" is {"requireAppointment":true,"sealMismatchAction":"FLAG","leaseSeconds":300}
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    Then the response status is 201
    And the response field "visit.appointmentId" is "appt-1"

  Scenario Outline: A site that requires appointments refuses an appointment that is no longer booked
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    And the yard policy of site "WH1" is {"requireAppointment":true,"sealMismatchAction":"FLAG","leaseSeconds":300}
    When dock-scheduling <fact>
    And the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    Then the response status is 409
    And the problem is "appointment-required"
    And no "VisitCheckedIn" event was published

    Examples:
      | fact                                       |
      | cancels appointment "appt-1"               |
      | records a no-show for appointment "appt-1" |

  Scenario: A booking that arrives late does not bring a cancelled appointment back
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    And the yard policy of site "WH1" is {"requireAppointment":true,"sealMismatchAction":"FLAG","leaseSeconds":300}
    When dock-scheduling cancels appointment "appt-1"
    And a stale booking of appointment "appt-1" arrives late
    Then appointment "appt-1" is marked CANCELLED
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    Then the response status is 409
    And the problem is "appointment-required"

  Scenario: An appointment booked for another site is not known here
    Given a yard site "WH2" with spots "WH2-Y01" and doors "WH2-D01:Both"
    And ASN "ASN-2" is registered
    And appointment "appt-w2" is booked at door "WH2-D01" for ASN "ASN-2" starting in 10 minutes
    And the gate works at site "WH1"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-w2"
    Then the response status is 422
    And the problem is "unknown-appointment"
    And no "VisitCheckedIn" event was published

  Scenario: One appointment covers exactly one trailer
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailers "TR-1,TR-2" (DRY_VAN, LOADED) on appointment "appt-1"
    Then the response status is 422
    And the problem is "unknown-appointment"
    When I send GET "/trailers"
    Then the response list "items" has 0 items

  # ---- DirectOnCheckIn: where the first move goes ----------------------------------------------------------------

  Scenario Outline: An arrival earlier than the appointment tolerance waits in a holding spot
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in <minutes> minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    Then the latest move of trailer "TR-1" has reason <reason> and performer VISITING_DRIVER
    And the latest move of trailer "TR-1" goes to <kind> "<code>"

    Examples:
      | minutes | reason     | kind | code    |
      | 15      | TO_DOOR    | DOOR | WH1-D01 |
      | 16      | TO_HOLDING | SPOT | WH1-Y01 |
      | -30     | TO_DOOR    | DOOR | WH1-D01 |

  Scenario Outline: An on-time arrival whose appointment door cannot take it goes to the first free spot
    Given ASN "ASN-1" is registered
    And <condition>
    And appointment "appt-1" is booked at door "<door>" for ASN "ASN-1" starting in 5 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    Then the response status is 201
    And the latest move of trailer "TR-1" has reason TO_SPOT and performer VISITING_DRIVER
    And the latest move of trailer "TR-1" goes to SPOT "WH1-Y01"

    Examples:
      | condition                                         | door    |
      | trailer "TR-0" is docked at door "WH1-D01"        | WH1-D01 |
      | the door "WH1-D01" is closed for the next 2 hours | WH1-D01 |
      | ASN "ASN-9" is registered                         | WH1-D03 |

  Scenario: A walk-in waits in the first free holding spot the site declares
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"holdingSpotCodes":["WH1-Y03","WH1-Y04"]}
    When the gate checks in visit "A" for purpose DROP with plate "AAA1" and trailer "TR-1" (DRY_VAN, LOADED)
    Then the latest move of trailer "TR-1" has reason TO_HOLDING and performer VISITING_DRIVER
    And the latest move of trailer "TR-1" goes to SPOT "WH1-Y03"
    When the gate checks in visit "B" for purpose DROP with plate "BBB2" and trailer "TR-2" (DRY_VAN, LOADED)
    Then the latest move of trailer "TR-2" goes to SPOT "WH1-Y04"

  Scenario: A holding spot must be a Yard slot of the site
    When I send PUT "/sites/WH1/yard-policy" with {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"holdingSpotCodes":["WH1-D01"]}
    Then the response status is 422
    And the problem is "unknown-location"
    When I send PUT "/sites/WH1/yard-policy" with {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"holdingSpotCodes":["WH9-Y77"]}
    Then the response status is 422
    And the problem is "unknown-location"
    When I send GET "/sites/WH1/yard-policy"
    Then the response field "declared" is false

  Scenario: The parking zones of the policy decide where an on-time arrival goes when its door is taken
    Given the site "WH1" has a spot "WH1-Z01" in zone "WH1-overflow"
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"parkingZoneIds":["WH1-overflow"]}
    And ASN "ASN-1" is registered
    And the door "WH1-D01" is closed for the next 2 hours
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 5 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    Then the latest move of trailer "TR-1" has reason TO_SPOT and performer VISITING_DRIVER
    And the latest move of trailer "TR-1" goes to SPOT "WH1-Z01"

  # ---- CheckOutVisit ---------------------------------------------------------------------------------------------

  Scenario: A check-out takes every trailer or none
    When the gate checks in visit "A" for purpose DROP with plate "TWO-1" and trailers "TR-1,TR-2" (DRY_VAN, LOADED)
    And the gate clerk completes the move of trailer "TR-1"
    And visit "A" checks out taking trailers "TR-1,TR-2"
    Then the response status is 409
    And the problem is "trailer-not-released"
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"
    And trailer "TR-2" is IN_MOVE
    And visit "A" is CHECKED_IN
    And no "TrailerDeparted" event was published
    When the gate clerk completes the move of trailer "TR-2"
    And visit "A" checks out taking trailers "TR-1,TR-2"
    Then the response status is 200
    And the response list "trailersOut" has 2 items
    And the event "TrailerDeparted" was published 2 times

  Scenario: A loaded trailer leaving a live load needs a seal and leaves from its door
    Given appointment "appt-9" is booked at door "WH1-D02" for shipment "SHP-9" starting in 5 minutes
    When the gate checks in visit "L" for purpose LIVE_LOAD with plate "LOAD1" and trailer "TR-1" (DRY_VAN, EMPTY) on appointment "appt-9"
    And the gate clerk completes the move of trailer "TR-1"
    Then trailer "TR-1" is DOCKED at DOOR "WH1-D02"
    When the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1" leaving it LOADED
    Then trailer "TR-1" has door work DONE and load state LOADED
    When visit "L" checks out taking trailers "TR-1"
    Then the response status is 409
    And the problem is "seal-required"
    And trailer "TR-1" is DOCKED at DOOR "WH1-D02"
    When the supervisor records seal "SL-77" on trailer "TR-1"
    And visit "L" checks out taking trailers "TR-1"
    Then the response status is 200
    And trailer "TR-1" is not on site
    And the published events include, in order, "VisitCheckedOut,TrailerUndocked,TrailerDeparted"

  Scenario: An empty trailer leaves a pickup without a seal
    Given trailer "TR-1" is parked empty
    When the gate checks in visit "P" for purpose PICKUP with plate "PU9"
    And visit "P" checks out taking trailers "TR-1"
    Then the response status is 200
    And trailer "TR-1" is not on site

  Scenario: A trailer that has left cannot leave again
    Given trailer "TR-1" has departed
    When the gate checks in visit "P2" for purpose PICKUP with plate "PU2"
    And visit "P2" checks out taking trailers "TR-1"
    Then the response status is 409
    And the problem is "trailer-not-released"

  Scenario: The departure is stamped with the clock and carries the dwell
    Given trailer "TR-1" is parked
    When the clock advances 90 minutes
    And the gate checks in visit "P" for purpose PICKUP with plate "PU9"
    And I send POST "/trailers/<trailer TR-1>/seals" with {"state":"INTACT","sealNumber":"SL-1"}
    And visit "P" checks out taking trailers "TR-1"
    Then the response field "departedAt" is "2026-11-05T15:30:00Z"
    And the latest "TrailerDeparted" event carries "dwell_minutes" 90
    When visit "TR-1" checks out taking no trailers
    Then the response field "departedAt" is "2026-11-05T15:30:00Z"
    And the latest "VisitCheckedOut" event carries "on_site_minutes" 90
