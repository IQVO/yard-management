@bdd
Feature: Dock safety interlock
  A trailer at a dock door is secured before anyone drives a forklift into it: a vehicle restraint engaged or the
  wheels chocked (OSHA 29 CFR 1910.178(k)(1), ANSI MH30.3). Where the site policy asks for it, door work does not
  start on a trailer that is not secured, and a secured trailer does not leave its door until the securing is
  released. A site that never turns the policy on behaves exactly as before.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03" and doors "WH1-D01:Inbound,WH1-D02:Both"

  Scenario: By default door work starts without any securing (behaviour unchanged)
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the dock operator starts door work on trailer "TR-1"
    Then the response status is 200
    And trailer "TR-1" has door work IN_PROGRESS and load state LOADED
    And trailer "TR-1" is not secured
    And no "TrailerSecured" event was published

  Scenario: A new trailer view carries no securing
    Given trailer "TR-1" is docked at door "WH1-D02"
    When I send GET "/sites/WH1/yard-policy"
    Then the response field "requireSecuringBeforeDoorWork" is false
    And trailer "TR-1" is not secured

  Scenario: Where the policy is on, door work is refused until the trailer is secured
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requireSecuringBeforeDoorWork":true}
    And trailer "TR-1" is docked at door "WH1-D02"
    When the dock operator starts door work on trailer "TR-1"
    Then the response status is 409
    And the problem is "trailer-not-secured"
    And trailer "TR-1" has door work WAITING and load state LOADED
    And no "DoorWorkStarted" event was published
    When the dock operator secures trailer "TR-1" with RESTRAINT
    Then the response status is 200
    And the response field "securedBy" is "RESTRAINT"
    And trailer "TR-1" is secured by RESTRAINT
    And the event "TrailerSecured" was published
    And the latest "TrailerSecured" event has field "method" "RESTRAINT"
    And the latest "TrailerSecured" event has field "door_code" "WH1-D02"
    When the dock operator starts door work on trailer "TR-1"
    Then the response status is 200
    And trailer "TR-1" has door work IN_PROGRESS and load state LOADED

  Scenario: Wheel chocks secure a trailer just as a restraint does, and the performer is optional
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requireSecuringBeforeDoorWork":true}
    And trailer "TR-1" is docked at door "WH1-D02"
    When I send POST "/trailers/<trailer TR-1>/securings" with {"method":"WHEEL_CHOCKS","securerRef":"dock-op-7"}
    Then the response status is 200
    And the response field "securedBy" is "WHEEL_CHOCKS"
    And the response field "securerRef" is "dock-op-7"
    When the dock operator starts door work on trailer "TR-1"
    Then the response status is 200

  Scenario: Securing the same way again changes nothing
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the dock operator secures trailer "TR-1" with RESTRAINT
    And the dock operator secures trailer "TR-1" with RESTRAINT
    Then the response status is 200
    And the event "TrailerSecured" was published 1 time
    And the response field "version" is 5

  Scenario: Securing a different way needs the first securing to be released
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the dock operator secures trailer "TR-1" with RESTRAINT
    And the dock operator secures trailer "TR-1" with WHEEL_CHOCKS
    Then the response status is 409
    And the problem is "illegal-transition"
    And trailer "TR-1" is secured by RESTRAINT

  Scenario: Only a docked trailer can be secured
    Given trailer "TR-1" is parked
    When the dock operator secures trailer "TR-1" with RESTRAINT
    Then the response status is 409
    And the problem is "illegal-transition"
    And no "TrailerSecured" event was published

  Scenario Outline: A securing method outside the two is a validation error
    Given trailer "TR-1" is docked at door "WH1-D02"
    When I send POST "/trailers/<trailer TR-1>/securings" with {"method":"<method>"}
    Then the response status is 422
    And the problem is "validation"

    Examples:
      | method  |
      | LOCKS   |
      | chocks  |
      |         |

  Scenario: A trailer with an open move off the door cannot be secured
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y02"
    Then the response status is 201
    When the dock operator secures trailer "TR-1" with RESTRAINT
    Then the response status is 409
    And the problem is "trailer-has-open-move"

  Scenario: A secured trailer is not moved away from its door until the securing is released
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the dock operator secures trailer "TR-1" with WHEEL_CHOCKS
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y02"
    Then the response status is 409
    And the problem is "trailer-still-secured"
    And trailer "TR-1" is DOCKED at DOOR "WH1-D02"
    When the dock operator releases the securing of trailer "TR-1"
    Then the response status is 200
    And trailer "TR-1" is not secured
    And the event "TrailerSecuringReleased" was published
    And the latest "TrailerSecuringReleased" event has field "door_code" "WH1-D02"
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y02"
    Then the response status is 201

  Scenario: A secured trailer cannot check out of the gate until the securing is released
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requireSecuringBeforeDoorWork":true}
    And ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And the gate clerk completes the move of trailer "TR-1"
    And the dock operator secures trailer "TR-1" with RESTRAINT
    And the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    And visit "A" checks out taking trailers "TR-1"
    Then the response status is 409
    And the problem is "trailer-still-secured"
    And trailer "TR-1" is DOCKED at DOOR "WH1-D01"
    When the dock operator releases the securing of trailer "TR-1"
    And visit "A" checks out taking trailers "TR-1"
    Then the response status is 200
    And trailer "TR-1" is not on site

  Scenario: The securing is kept while door work is in progress
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the dock operator secures trailer "TR-1" with RESTRAINT
    And the dock operator starts door work on trailer "TR-1"
    And the dock operator releases the securing of trailer "TR-1"
    Then the response status is 409
    And the problem is "door-work-open"
    And trailer "TR-1" is secured by RESTRAINT

  Scenario: There is nothing to release on a trailer that is not secured
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the dock operator releases the securing of trailer "TR-1"
    Then the response status is 409
    And the problem is "trailer-not-secured"

  Scenario: Closing the receipt completes the unload without the interlock
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requireSecuringBeforeDoorWork":true}
    And ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    And receipt "rcpt-1" is opened for ASN "ASN-1" on appointment "appt-1" at door "WH1-D01"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And the gate clerk completes the move of trailer "TR-1"
    And inbound-receiving closes receipt "rcpt-1" for ASN "ASN-1"
    Then trailer "TR-1" has door work DONE and load state EMPTY
    And trailer "TR-1" is not secured
    And the event "DoorWorkStarted" was published

  Scenario: A secured drop trailer is cleared from its door once the securing is released
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the dock operator secures trailer "TR-1" with RESTRAINT
    And the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    Then trailer "TR-1" has door work DONE and load state EMPTY
    And trailer "TR-1" has no open move
    When the dock operator releases the securing of trailer "TR-1"
    Then the response status is 200
    And the latest move of trailer "TR-1" has reason FROM_DOOR and performer HOSTLER

  Scenario: Replacing the policy without the field turns the interlock off again
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requireSecuringBeforeDoorWork":true}
    When I send GET "/sites/WH1/yard-policy"
    Then the response field "requireSecuringBeforeDoorWork" is true
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300}
    And trailer "TR-1" is docked at door "WH1-D02"
    When the dock operator starts door work on trailer "TR-1"
    Then the response status is 200
