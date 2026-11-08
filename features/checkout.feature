@bdd
Feature: Check-out
  A visit leaves only with trailers that are on site and released: no open move, and either parked or docked with the
  door work done. A loaded trailer leaving on a pickup or a live load carries a seal.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03" and doors "WH1-D01:Inbound,WH1-D02:Both"

  Scenario: A trailer still in a move is not released
    When the gate checks in visit "A" for purpose DROP with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED)
    And visit "A" checks out taking trailers "TR-1"
    Then the response status is 409
    And the problem is "trailer-not-released"
    And visit "A" is CHECKED_IN
    And no "VisitCheckedOut" event was published

  Scenario: A docked trailer whose door work is open is not released
    Given trailer "TR-1" is docked at door "WH1-D02"
    When visit "TR-1" checks out taking trailers "TR-1"
    Then the response status is 409
    And trailer "TR-1" is DOCKED at DOOR "WH1-D02"
    And visit "TR-1" is CHECKED_IN

  Scenario: A trailer that is not on site is not released
    Given trailer "TR-1" is parked
    When I send POST "/visits/<visit TR-1>/check-outs" with {"trailersOut":[{"trailerId":"trl-nope"}]}
    Then the response status is 409
    And the problem is "trailer-not-released"

  Scenario: A pickup of a loaded trailer needs a seal
    Given trailer "TR-1" is parked
    When the gate checks in visit "P" for purpose PICKUP with plate "PU1"
    Then the response status is 201
    And the response list "trailers" has 0 items
    When visit "P" checks out taking trailers "TR-1"
    Then the response status is 409
    And the problem is "seal-required"
    When I send POST "/trailers/<trailer TR-1>/seals" with {"state":"INTACT","sealNumber":"SL-5"}
    Then the response status is 200
    And the response field "seal.number" is "SL-5"
    When visit "P" checks out taking trailers "TR-1"
    Then the response status is 200
    And the response field "trailersOut.0" is "trl-000001"
    And trailer "TR-1" is not on site
    And the event "TrailerDeparted" was published
    And the event "VisitCheckedOut" was published

  Scenario: A drop leaves the trailer behind
    Given trailer "TR-1" is parked
    When visit "TR-1" checks out taking no trailers
    Then the response status is 200
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"

  Scenario: A visit checks out once
    Given trailer "TR-1" is parked
    When visit "TR-1" checks out taking no trailers
    And visit "TR-1" checks out taking no trailers
    Then the response status is 409
    And the problem is "illegal-transition"

  Scenario: A check-out without the list of trailers is malformed
    Given trailer "TR-1" is parked
    When I send POST "/visits/<visit TR-1>/check-outs" with {}
    Then the response status is 400
    And the problem is "malformed-request-body"
