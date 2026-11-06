@bdd
Feature: Rejection at the gate
  A refused check-in creates nothing. The gate clerk records a turn-away with a visit rejection, and a seal mismatch under
  the REJECT policy records one by itself. Either way the yard publishes VisitRejected.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03" and doors "WH1-D01:Inbound,WH1-D02:Both"
    And ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes with expected seal "SL-1"

  Scenario: The clerk turns a visit away
    When the gate rejects a visit with plate "NOPE1" and driver "drv-9" because SITE_REQUEST
    Then the response status is 201
    And the response header "Location" is "/visits/visit-000001"
    And the response field "state" is "REJECTED"
    And the response field "rejectionReason" is "SITE_REQUEST"
    And the response field "driverRef" is "drv-9"
    And the response list "trailersIn" has 0 items
    And the event "VisitRejected" was published 1 time
    And no "VisitCheckedIn" event was published

  Scenario: A reason outside the catalogue is refused
    When the gate rejects a visit with plate "NOPE1" because BECAUSE
    Then the response status is 422
    And the problem is "validation"
    And no "VisitRejected" event was published

  Scenario: A site that requires appointments refuses a walk-in and persists nothing
    Given the yard policy of site "WH1" is {"requireAppointment":true,"sealMismatchAction":"FLAG","leaseSeconds":300}
    When the gate checks in visit "W" for purpose DROP with plate "WALK1" and trailer "TR-9" (DRY_VAN, EMPTY)
    Then the response status is 409
    And the problem is "appointment-required"
    And no "VisitCheckedIn" event was published
    When I send GET "/visits"
    Then the response list "items" has 0 items
    When I send GET "/trailers"
    Then the response list "items" has 0 items
    When the gate rejects a visit with plate "WALK1" because APPOINTMENT_REQUIRED
    Then the response field "state" is "REJECTED"
    And the response field "rejectionReason" is "APPOINTMENT_REQUIRED"
    And the event "VisitRejected" was published

  Scenario: A seal mismatch under the FLAG policy raises the exception and admits the visit
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1" sealed "SL-9"
    Then the response status is 201
    And the response field "visit.state" is "CHECKED_IN"
    And the event "SealExceptionRaised" was published 1 time
    And the event "VisitCheckedIn" was published
    And no "VisitRejected" event was published

  Scenario: A seal mismatch under the REJECT policy records a rejected visit in the same call
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"REJECT","leaseSeconds":300}
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1" sealed "SL-9"
    Then the response status is 201
    And the response field "visit.state" is "REJECTED"
    And the response field "visit.rejectionReason" is "SEAL_MISMATCH"
    And the response list "trailers" has 0 items
    And the response list "moves" has 0 items
    And the published events include, in order, "SealExceptionRaised,VisitRejected"
    And no "VisitCheckedIn" event was published
    When I send GET "/trailers"
    Then the response list "items" has 0 items

  Scenario: A matching seal raises nothing
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"REJECT","leaseSeconds":300}
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1" sealed "SL-1"
    Then the response field "visit.state" is "CHECKED_IN"
    And no "SealExceptionRaised" event was published

  Scenario: The same trailer cannot be on site twice
    Given trailer "TR-1" is parked
    When the gate checks in visit "B" for purpose DROP with plate "OTHER1" and trailer "TR-1" (DRY_VAN, LOADED)
    Then the response status is 409
    And the problem is "trailer-already-on-site"
    When the gate rejects a visit with plate "OTHER1" because TRAILER_ALREADY_ON_SITE
    Then the response field "rejectionReason" is "TRAILER_ALREADY_ON_SITE"

  Scenario: One open visit per tractor plate
    Given trailer "TR-1" is parked
    When the gate checks in visit "B" for purpose BOBTAIL with plate "PLATE-TR-1"
    Then the response status is 409
    And the problem is "visit-already-open"

  Scenario: An unknown appointment is a reference error
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-nope"
    Then the response status is 422
    And the problem is "unknown-appointment"
