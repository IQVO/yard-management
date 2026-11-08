@bdd
Feature: Drop
  A driver drops a trailer and leaves with the tractor. The trailer waits in a holding spot, a supervisor sends it to a
  door, the dock works it and a hostler clears the door. The next trailer of that door is called in.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03" and doors "WH1-D01:Inbound,WH1-D02:Both"

  Scenario: A walk-in drop trailer is held, docked, worked, cleared and the tractor leaves without it
    When the gate checks in visit "A" for purpose DROP with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED)
    Then the response status is 201
    And the latest move of trailer "TR-1" has reason TO_HOLDING and performer VISITING_DRIVER
    And the latest move of trailer "TR-1" goes to SPOT "WH1-Y01"
    When the gate clerk completes the move of trailer "TR-1"
    Then trailer "TR-1" is PARKED at SPOT "WH1-Y01"
    And the event "TrailerParked" was published
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 201
    And the response field "performer" is "HOSTLER"
    And the response field "state" is "REQUESTED"
    And the response field "reason" is "TO_DOOR"
    When hostler "h1" claims the next move at site "WH1"
    Then the response status is 201
    And the response field "hostlerRef" is "h1"
    And trailer "TR-1" is IN_MOVE
    When hostler "h1" completes the move of trailer "TR-1"
    Then trailer "TR-1" is DOCKED at DOOR "WH1-D02"
    When the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    Then trailer "TR-1" has door work DONE and load state EMPTY
    And the latest move of trailer "TR-1" has reason FROM_DOOR and performer HOSTLER
    And the latest move of trailer "TR-1" goes to SPOT "WH1-Y01"
    When hostler "h1" claims the next move at site "WH1"
    Then trailer "TR-1" is IN_MOVE
    And the event "TrailerUndocked" was published
    When hostler "h1" completes the move of trailer "TR-1"
    Then trailer "TR-1" is PARKED at SPOT "WH1-Y01"
    When visit "A" checks out taking no trailers
    Then the response status is 200
    And the response field "state" is "CHECKED_OUT"
    And the response list "trailersOut" has 0 items
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"

  Scenario: A door that frees up calls in the next trailer of its appointments
    Given ASN "ASN-2" is registered
    And appointment "appt-2" is booked at door "WH1-D01" for ASN "ASN-2" starting in 120 minutes
    And trailer "TR-1" is docked at door "WH1-D01"
    When the gate checks in visit "B" for purpose DROP with plate "XYZ9" and trailer "TR-2" (DRY_VAN, LOADED) on appointment "appt-2"
    Then the latest move of trailer "TR-2" has reason TO_HOLDING and performer VISITING_DRIVER
    When the gate clerk completes the move of trailer "TR-2"
    Then trailer "TR-2" is PARKED
    When the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    And hostler "h1" claims the next move at site "WH1"
    Then trailer "TR-1" is IN_MOVE
    And the latest move of trailer "TR-2" has reason TO_DOOR and performer HOSTLER
    And the latest move of trailer "TR-2" goes to DOOR "WH1-D01"
    And the latest move of trailer "TR-2" is REQUESTED

  Scenario: A drop trailer with no free spot queues at the gate
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    And trailer "TR-3" is parked
    When the gate checks in visit "D" for purpose DROP with plate "LAST1" and trailer "TR-4" (DRY_VAN, EMPTY)
    Then the response status is 409
    And the problem is "no-free-spot"
    And the event "VisitCheckedIn" was published 3 times
