@bdd
Feature: Live unload
  A carrier arrives on time for an inbound appointment. The driver takes the trailer straight to the appointment door,
  the dock unloads it and the visit leaves from the door. The yard publishes every step as a fact.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03" and doors "WH1-D01:Inbound,WH1-D02:Both"
    And ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes with expected seal "SL-1"

  Scenario: The whole live unload, from the gate in to the gate out
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1" sealed "SL-1"
    Then the response status is 201
    And the response header "Location" is "/visits/visit-000001"
    And the response field "visit.state" is "CHECKED_IN"
    And the response field "visit.appointmentId" is "appt-1"
    And the response field "moves.0.performer" is "VISITING_DRIVER"
    And the response field "moves.0.state" is "CLAIMED"
    And the response field "moves.0.reason" is "TO_DOOR"
    And the response field "moves.0.to.code" is "WH1-D01"
    And the response field "moves.0.leaseExpiresAt" is null
    And no "SealExceptionRaised" event was published
    And appointment "appt-1" is marked CHECKED_IN
    When the gate clerk completes the move of trailer "TR-1"
    Then the response status is 200
    And trailer "TR-1" is DOCKED at DOOR "WH1-D01"
    And trailer "TR-1" has door work WAITING and load state LOADED
    And the event "TrailerDocked" was published
    When the dock operator starts door work on trailer "TR-1"
    Then trailer "TR-1" has door work IN_PROGRESS and load state LOADED
    When the dock operator completes door work on trailer "TR-1"
    Then trailer "TR-1" has door work DONE and load state EMPTY
    And the event "DoorWorkCompleted" was published
    When visit "A" checks out taking trailers "TR-1"
    Then the response status is 200
    And the response field "state" is "CHECKED_OUT"
    And the response field "trailersOut.0" is "trl-000001"
    And trailer "TR-1" is not on site
    And appointment "appt-1" is marked USED
    And the published events include, in order, "VisitCheckedIn,YardMoveRequested,TrailerDocked,DoorWorkStarted,DoorWorkCompleted,VisitCheckedOut,TrailerUndocked,TrailerDeparted"

  Scenario: Closing the receipt completes the unload by itself
    Given receipt "rcpt-1" is opened for ASN "ASN-1" on appointment "appt-1" at door "WH1-D01"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1" sealed "SL-1"
    And the gate clerk completes the move of trailer "TR-1"
    And the dock operator starts door work on trailer "TR-1"
    And inbound-receiving closes receipt "rcpt-1" for ASN "ASN-1"
    Then trailer "TR-1" has door work DONE and load state EMPTY
    When visit "A" checks out taking trailers "TR-1"
    Then the response status is 200

  Scenario: The visit cannot leave while the unload is still open
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1" sealed "SL-1"
    And the gate clerk completes the move of trailer "TR-1"
    And visit "A" checks out taking trailers "TR-1"
    Then the response status is 409
    And trailer "TR-1" is DOCKED at DOOR "WH1-D01"

  Scenario: The trailer carries the ASNs of its appointment
    Given ASN "ASN-2" is registered
    And appointment "appt-2" is booked at door "WH1-D02" for ASN "ASN-2" starting in 10 minutes
    When the gate checks in visit "B" for purpose LIVE_UNLOAD with plate "XYZ9" and trailer "TR-2" (DRY_VAN, LOADED) on appointment "appt-2"
    Then the response status is 201
    And the response field "trailers.0.asnNumbers.0" is "ASN-2"
