@bdd
Feature: Move leases
  A hostler pulls the next move and holds it on a lease. When the lease runs out the sweep returns the move to the queue
  and the trailer to where it was, so another hostler can take it.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03" and doors "WH1-D01:Inbound,WH1-D02:Both"
    And trailer "TR-1" is parked

  Scenario: A lease that has not run out is kept
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    And hostler "h1" claims the next move at site "WH1"
    Then the response status is 201
    And the response field "state" is "CLAIMED"
    And the response field "leaseExpiresAt" is "2026-11-05T14:05:00Z"
    When the clock advances 299 seconds
    And the lease expiry sweep runs
    Then the response status is 200
    And the response field "count" is 0
    And the latest move of trailer "TR-1" is CLAIMED
    And trailer "TR-1" is IN_MOVE

  Scenario: An expired lease returns the move to the queue and the trailer to its spot
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    And hostler "h1" claims the next move at site "WH1"
    And the clock advances 301 seconds
    And the lease expiry sweep runs
    Then the response field "count" is 1
    And the latest move of trailer "TR-1" is REQUESTED
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"
    And the event "YardMoveLeaseExpired" was published
    When hostler "h1" completes the move of trailer "TR-1"
    Then the response status is 409
    And the problem is "illegal-transition"
    When hostler "h2" claims the next move at site "WH1"
    Then the response status is 201
    And the response field "hostlerRef" is "h2"
    When hostler "h2" completes the move of trailer "TR-1"
    Then trailer "TR-1" is PARKED at SPOT "WH1-Y03"

  Scenario: Only the lease holder completes a move
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    And hostler "h1" claims the next move at site "WH1"
    And hostler "h2" completes the move of trailer "TR-1"
    Then the response status is 409
    And the problem is "not-lease-holder"
    And the latest move of trailer "TR-1" is CLAIMED

  Scenario: A hostler holds one move at a time
    Given trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    And the supervisor requests a move of trailer "TR-2" to DOOR "WH1-D02"
    And hostler "h1" claims the next move at site "WH1"
    And hostler "h1" claims the next move at site "WH1"
    Then the response status is 409
    And the problem is "hostler-busy"

  Scenario: Nothing waits for the hostler
    When hostler "h1" claims the next move at site "WH1"
    Then the response status is 404
    And the problem is "no-claimable-move"

  Scenario: The next move is the one with the lowest band, then the earliest due time
    Given trailer "TR-2" is parked
    When I send POST "/moves" with {"trailerId":"<trailer TR-1>","to":{"kind":"SPOT","code":"WH1-Y03"},"priorityBand":7}
    And I send POST "/moves" with {"trailerId":"<trailer TR-2>","to":{"kind":"DOOR","code":"WH1-D02"},"priorityBand":2}
    And hostler "h1" claims the next move at site "WH1"
    Then the response field "trailerId" is "trl-000002"
    And the response field "priority.band" is 2

  Scenario: Cancelling a claimed move gives the trailer back
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    And hostler "h1" claims the next move at site "WH1"
    And the supervisor cancels the move of trailer "TR-1"
    Then the response status is 200
    And the response field "state" is "CANCELLED"
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"
