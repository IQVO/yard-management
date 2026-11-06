@bdd
Feature: Occupancy
  A spot or a door holds at most one trailer, a destination is reserved when the move is requested, and a trailer has at
  most one open move. Whoever loses a race is told so with a 409, never with a server error.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03,WH1-Y04" and doors "WH1-D01:Inbound,WH1-D02:Both,WH1-D03:Outbound"

  Scenario: Two supervisors race for one door and exactly one wins
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When supervisors concurrently request moves of trailers "TR-1" and "TR-2" to DOOR "WH1-D02"
    Then exactly one concurrent request succeeded and the other was refused with 409

  Scenario Outline: The same race for any destination has one winner
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When supervisors concurrently request moves of trailers "TR-1" and "TR-2" to <kind> "<code>"
    Then exactly one concurrent request succeeded and the other was refused with 409

    Examples:
      | kind | code    |
      | DOOR | WH1-D01 |
      | DOOR | WH1-D02 |
      | SPOT | WH1-Y03 |
      | SPOT | WH1-Y04 |

  Scenario: A destination already reserved by an open move is refused
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    And the supervisor requests a move of trailer "TR-2" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "destination-reserved"

  Scenario: A door that holds a trailer takes no other
    Given trailer "TR-1" is docked at door "WH1-D02"
    And trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-2" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "location-occupied"

  Scenario: A trailer has at most one open move
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    Then the response status is 409
    And the problem is "trailer-has-open-move"

  Scenario: Arriving at an occupied location is refused and the move stays claimed
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1"
    And hostler "h1" completes the move of trailer "TR-1" at SPOT "WH1-Y02" noting "the spot looked free"
    Then the response status is 409
    And the problem is "location-occupied"
    And the latest move of trailer "TR-1" is CLAIMED
    And trailer "TR-1" is IN_MOVE

  Scenario: Completing at a place another open move has reserved is refused
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    And the supervisor requests a move of trailer "TR-2" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1"
    And hostler "h1" completes the move of trailer "TR-1" at SPOT "WH1-Y04" noting "the driver took the wrong turn"
    Then the response status is 409
    And the problem is "destination-reserved"
    And the latest move of trailer "TR-1" is CLAIMED

  Scenario: A closed door takes no trailer
    Given trailer "TR-1" is parked
    And the door "WH1-D02" is closed for the next 2 hours
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "door-closed"

  Scenario: An outbound door takes no trailer that holds an inbound ASN
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 120 minutes
    When the gate checks in visit "A" for purpose DROP with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And the gate clerk completes the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D03"
    Then the response status is 409
    And the problem is "door-flow-mismatch"

  Scenario: A held trailer is not docked
    Given trailer "TR-1" is parked
    When the supervisor places a DAMAGED hold on trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "trailer-on-hold"
    When the supervisor releases the hold on trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 201

  Scenario: A place the site does not know is refused
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH9-D77"
    Then the response status is 422
    And the problem is "unknown-location"
