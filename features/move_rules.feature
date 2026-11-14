@bdd
Feature: Move rules from the documentation
  What the use cases, the aggregate canvas and ADR 0002 say about RequestMove, ClaimNextMove, CompleteMove, CancelMove,
  ExpireLease and the policies that create or cancel moves (CallToDoor, NoShowFreesHolding), and about the facts the
  yard folds into its local copies: the claim order, the lease, the reservation of a place, the origin of a claimed
  trailer, the placement judged again on arrival, and the redelivery and ordering of consumed events.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03,WH1-Y04" and doors "WH1-D01:Inbound,WH1-D02:Both,WH1-D03:Outbound"

  # ---- RequestMove ------------------------------------------------------------------------------------------------

  Scenario Outline: A move that names no reason gets the one its destination implies
    Given trailer "TR-1" is <situation>
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    Then the response status is 201
    And the response field "reason" is "<reason>"

    Examples:
      | situation                | reason    |
      | parked                   | RELOCATE  |
      | docked at door "WH1-D02" | FROM_DOOR |

  Scenario Outline: A priority band from 1 to 9 is kept
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04" with priority band <band>
    Then the response status is 201
    And the response field "priority.band" is <band>

    Examples:
      | band |
      | 1    |
      | 9    |

  Scenario Outline: A priority band outside 1 to 9 is refused
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04" with priority band <band>
    Then the response status is 422
    And the problem is "validation"

    Examples:
      | band |
      | 10   |
      | -1   |

  Scenario: A move that names no band gets band 5
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    Then the response field "priority.band" is 5
    And the response field "performer" is "HOSTLER"

  Scenario: A move for a trailer the yard does not know is not found
    When I send POST "/moves" with {"trailerId":"trl-nope","to":{"kind":"SPOT","code":"WH1-Y04"}}
    Then the response status is 404
    And the problem is "trailer-not-found"

  Scenario: A place that holds a trailer is free for another request as soon as that trailer is claimed
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And the supervisor requests a move of trailer "TR-2" to SPOT "WH1-Y01"
    Then the response status is 409
    And the problem is "location-occupied"
    When hostler "h1" claims the next move at site "WH1"
    Then trailer "TR-1" is IN_MOVE
    And trailer "TR-1" has no place
    When the supervisor requests a move of trailer "TR-2" to SPOT "WH1-Y01"
    Then the response status is 201

  # ---- ClaimNextMove ----------------------------------------------------------------------------------------------

  Scenario: At equal band the claim takes the earliest due time and a move without one comes last
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    And trailer "TR-3" is parked
    When I send POST "/moves" with {"trailerId":"<trailer TR-1>","to":{"kind":"SPOT","code":"WH1-Y04"}}
    And I send POST "/moves" with {"trailerId":"<trailer TR-2>","to":{"kind":"DOOR","code":"WH1-D02"},"dueBy":"2026-11-05T16:00:00Z"}
    And I send POST "/moves" with {"trailerId":"<trailer TR-3>","to":{"kind":"DOOR","code":"WH1-D01"},"dueBy":"2026-11-05T15:00:00Z"}
    Then hostler "h1" claims the next move at site "WH1" and gets the move of trailer "TR-3"
    And hostler "h2" claims the next move at site "WH1" and gets the move of trailer "TR-2"
    And hostler "h3" claims the next move at site "WH1" and gets the move of trailer "TR-1"

  Scenario: At equal band and without due times the claim takes the oldest request
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-2" to SPOT "WH1-Y04"
    And the clock advances 60 seconds
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then hostler "h1" claims the next move at site "WH1" and gets the move of trailer "TR-2"
    And hostler "h2" claims the next move at site "WH1" and gets the move of trailer "TR-1"

  Scenario: A hostler claims only the moves of its own site
    Given a yard site "WH2" with spots "WH2-Y01" and doors "WH2-D01:Both"
    And the gate works at site "WH1"
    And trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH2"
    Then the response status is 404
    And the problem is "no-claimable-move"
    When hostler "h1" claims the next move at site "WH1" and gets the move of trailer "TR-1"
    Then the response status is 201

  Scenario: The first move from the gate belongs to the visiting driver and is not up for claim
    When the gate checks in visit "A" for purpose DROP with plate "AAA1" and trailer "TR-1" (DRY_VAN, LOADED)
    And hostler "h1" claims the next move at site "WH1"
    Then the response status is 404
    And the problem is "no-claimable-move"
    And the latest move of trailer "TR-1" is CLAIMED

  Scenario: The lease lasts as long as the policy of the site says
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":120}
    And trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1"
    Then the response field "leaseExpiresAt" is "2026-11-05T14:02:00Z"
    When the clock advances 119 seconds
    And the lease expiry sweep runs
    Then the response field "count" is 0
    When the clock advances 2 seconds
    And the lease expiry sweep runs
    Then the response field "count" is 1
    And the latest move of trailer "TR-1" is REQUESTED

  Scenario Outline: A claim names a hostler of 1 to 64 characters
    When I send POST "/moves/claims" with {"siteId":"WH1","hostlerRef":"<ref>"}
    Then the response status is <status>
    And the problem is "<problem>"

    Examples:
      | ref                                                               | status | problem           |
      |                                                                   | 422    | validation        |
      | hhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhh | 422    | validation        |
      | hhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhh  | 404    | no-claimable-move |

  # ---- CompleteMove and the visiting driver ----------------------------------------------------------------------

  Scenario: A completion replayed with the same Idempotency-Key is answered once
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1"
    And I send POST "/moves/mv-000002/completions" with the Idempotency-Key "done-1" and body {"hostlerRef":"h1"}
    And I send POST "/moves/mv-000002/completions" with the Idempotency-Key "done-1" and body {"hostlerRef":"h1"}
    Then the response status is 200
    And the response header "Idempotency-Replayed" is "true"
    And the event "YardMoveCompleted" was published 2 times
    When I send POST "/moves/mv-000002/completions" with the Idempotency-Key "done-2" and body {"hostlerRef":"h1"}
    Then the response status is 409
    And the problem is "illegal-transition"

  Scenario: A visiting driver's move has no lease: it neither expires nor is cancelled and anyone at the gate completes it
    When the gate checks in visit "A" for purpose DROP with plate "AAA1" and trailer "TR-1" (DRY_VAN, LOADED)
    And the clock advances 3600 seconds
    And the lease expiry sweep runs
    Then the response field "count" is 0
    And the latest move of trailer "TR-1" is CLAIMED
    When the supervisor cancels the move of trailer "TR-1"
    Then the response status is 409
    And the problem is "illegal-transition"
    And the latest move of trailer "TR-1" is CLAIMED
    When hostler "h9" completes the move of trailer "TR-1"
    Then the response status is 200
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"

  Scenario: A hostler who completed its move may claim the next one
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And the supervisor requests a move of trailer "TR-2" to DOOR "WH1-D02"
    And hostler "h1" claims the next move at site "WH1" and gets the move of trailer "TR-1"
    And hostler "h1" claims the next move at site "WH1"
    Then the response status is 409
    And the problem is "hostler-busy"
    When hostler "h1" completes the move of trailer "TR-1"
    Then hostler "h1" claims the next move at site "WH1" and gets the move of trailer "TR-2"

  # ---- CancelMove -------------------------------------------------------------------------------------------------

  Scenario: Cancelling a requested move releases its destination and leaves the trailer where it is
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    And the supervisor requests a move of trailer "TR-2" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "destination-reserved"
    When the supervisor cancels the move of trailer "TR-1"
    Then the response status is 200
    And the response field "state" is "CANCELLED"
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"
    When the supervisor requests a move of trailer "TR-2" to DOOR "WH1-D02"
    Then the response status is 201

  Scenario: A move cancelled twice stays cancelled and is announced once
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And the supervisor cancels the move of trailer "TR-1"
    And I send POST "/moves/mv-000002/cancellations" with {"reason":"again"}
    Then the latest move of trailer "TR-1" is CANCELLED
    And the event "YardMoveCancelled" was published 1 time

  # ---- ExpireLease: where the trailer goes back to -----------------------------------------------------------------

  Scenario: A lease that expires while the origin has been taken leaves the trailer in motion and the move in the queue
    Given trailer "TR-1" is parked
    And trailer "TR-2" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1"
    And the supervisor requests a move of trailer "TR-2" to SPOT "WH1-Y01"
    And hostler "h2" claims the next move at site "WH1"
    And hostler "h2" completes the move of trailer "TR-2"
    Then trailer "TR-2" is PARKED at SPOT "WH1-Y01"
    When the clock advances 301 seconds
    And the lease expiry sweep runs
    Then the response field "count" is 1
    And the latest move of trailer "TR-1" is REQUESTED
    And trailer "TR-1" is IN_MOVE
    When hostler "h3" claims the next move at site "WH1" and gets the move of trailer "TR-1"
    And hostler "h3" completes the move of trailer "TR-1"
    Then trailer "TR-1" is PARKED at SPOT "WH1-Y04"

  Scenario: A lease that expires returns a docked trailer to its door and announces it again
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1"
    Then the event "TrailerUndocked" was published 1 time
    When the clock advances 301 seconds
    And the lease expiry sweep runs
    Then the response field "count" is 1
    And trailer "TR-1" is DOCKED at DOOR "WH1-D02"
    And trailer "TR-1" has door work WAITING and load state LOADED
    And the event "TrailerDocked" was published 2 times

  # ---- ArriveAt: the place is judged again on arrival ----------------------------------------------------------------

  Scenario: The door rules are judged again when the trailer arrives
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    And hostler "h1" claims the next move at site "WH1"
    And the door "WH1-D02" is closed for the next 2 hours
    And hostler "h1" completes the move of trailer "TR-1"
    Then the response status is 409
    And the problem is "door-closed"
    And the latest move of trailer "TR-1" is CLAIMED
    And trailer "TR-1" is IN_MOVE
    When the closure of door "WH1-D02" is lifted
    And the supervisor places a QUALITY hold on trailer "TR-1"
    And hostler "h1" completes the move of trailer "TR-1"
    Then the response status is 409
    And the problem is "trailer-on-hold"
    When the supervisor releases the hold on trailer "TR-1"
    And hostler "h1" completes the move of trailer "TR-1"
    Then the response status is 200
    And trailer "TR-1" is DOCKED at DOOR "WH1-D02"

  Scenario: A decommissioned slot is no destination and a redelivered registration does not bring it back
    Given facility-layout delivers event "evt-slot-1" registering spot "WH1-Y09" in zone "WH1-yard"
    And facility-layout delivers event "evt-gone-1" decommissioning slot "WH1-Y09"
    And trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y09"
    Then the response status is 422
    And the problem is "unknown-location"
    When facility-layout delivers event "evt-slot-1" registering spot "WH1-Y09" in zone "WH1-yard"
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y09"
    Then the response status is 422
    And the problem is "unknown-location"
    When facility-layout delivers event "evt-slot-2" registering spot "WH1-Y09" in zone "WH1-yard"
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y09"
    Then the response status is 201

  Scenario: A slot that arrives before its zone is held until the zone is known
    Given facility-layout delivers event "evt-slot-5" registering spot "WH5-Y01" in zone "WH5-yard"
    And the gate works at site "WH5"
    When the gate checks in visit "A" for purpose DROP with plate "WH5-1" and trailer "TR-1" (DRY_VAN, LOADED)
    Then the response status is 409
    And the problem is "no-free-spot"
    When facility-layout delivers event "evt-zone-5" registering zone "WH5-yard" for site "WH5"
    And the gate checks in visit "A" for purpose DROP with plate "WH5-1" and trailer "TR-1" (DRY_VAN, LOADED)
    Then the response status is 201
    And the latest move of trailer "TR-1" goes to SPOT "WH5-Y01"

  Scenario: A door that was closed takes trailers again once the closure is lifted
    Given trailer "TR-1" is parked
    And the door "WH1-D02" is closed for the next 2 hours
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "door-closed"
    When the closure of door "WH1-D02" is lifted
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 201

  Scenario Outline: A door is bound to a direction only for a trailer that is loaded, or that is loaded by its visit
    When the gate checks in visit "A" for purpose <purpose> with plate "AAA1" and trailer "TR-1" (DRY_VAN, <load>)
    And the gate clerk completes the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "<door>"
    Then the response status is <status>

    Examples:
      | purpose   | load   | door    | status |
      | DROP      | EMPTY  | WH1-D03 | 201    |
      | DROP      | EMPTY  | WH1-D01 | 201    |
      | DROP      | LOADED | WH1-D03 | 201    |
      | LIVE_LOAD | EMPTY  | WH1-D01 | 409    |
      | LIVE_LOAD | EMPTY  | WH1-D03 | 201    |

  # ---- Policies of the yard: CallToDoor and NoShowFreesHolding -------------------------------------------------------

  Scenario Outline: A door that frees up calls the waiting trailer with a priority that grows with the time left
    Given ASN "ASN-2" is registered
    And appointment "appt-2" is booked at door "WH1-D01" for ASN "ASN-2" starting in <minutes> minutes
    And trailer "TR-1" is docked at door "WH1-D01"
    When the gate checks in visit "B" for purpose DROP with plate "XYZ9" and trailer "TR-2" (DRY_VAN, LOADED) on appointment "appt-2"
    And the gate clerk completes the move of trailer "TR-2"
    And the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    And hostler "h1" claims the next move at site "WH1"
    Then the latest move of trailer "TR-2" has reason TO_DOOR and performer HOSTLER
    And the latest move of trailer "TR-2" has priority band <band> due in <minutes> minutes

    Examples:
      | minutes | band |
      | 16      | 5    |
      | 30      | 5    |
      | 31      | 6    |
      | 60      | 6    |
      | 61      | 7    |
      | 120     | 7    |
      | 121     | 8    |
      | 240     | 8    |
      | 241     | 9    |

  Scenario Outline: An appointment that is cancelled or missed frees the door reserved for its trailer
    Given ASN "ASN-2" is registered
    And appointment "appt-2" is booked at door "WH1-D02" for ASN "ASN-2" starting in 60 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-2"
    And the gate clerk completes the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    And dock-scheduling <fact>
    Then the latest move of trailer "TR-1" is CANCELLED
    And the event "YardMoveCancelled" was published 1 time
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"
    When trailer "TR-2" is parked
    And the supervisor requests a move of trailer "TR-2" to DOOR "WH1-D02"
    Then the response status is 201

    Examples:
      | fact                                       |
      | cancels appointment "appt-2"               |
      | records a no-show for appointment "appt-2" |

  Scenario: A claimed move for a cancelled appointment gives the trailer back to its spot
    Given ASN "ASN-2" is registered
    And appointment "appt-2" is booked at door "WH1-D02" for ASN "ASN-2" starting in 60 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-2"
    And the gate clerk completes the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    And hostler "h1" claims the next move at site "WH1"
    Then trailer "TR-1" is IN_MOVE
    When dock-scheduling cancels appointment "appt-2"
    Then the latest move of trailer "TR-1" is CANCELLED
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"

  Scenario: The visiting driver's move to the door survives a cancelled appointment
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 5 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And dock-scheduling cancels appointment "appt-1"
    Then the latest move of trailer "TR-1" is CLAIMED
    And no "YardMoveCancelled" event was published
    When the gate clerk completes the move of trailer "TR-1"
    Then trailer "TR-1" is DOCKED at DOOR "WH1-D01"

  Scenario: A trailer already at its door stays there when the appointment is cancelled
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 5 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And the gate clerk completes the move of trailer "TR-1"
    And dock-scheduling cancels appointment "appt-1"
    Then trailer "TR-1" is DOCKED at DOOR "WH1-D01"
    And no "YardMoveCancelled" event was published

  Scenario: A cancelled appointment cancels the move to its door and leaves a relocation of the trailer alone
    Given ASN "ASN-2" is registered
    And appointment "appt-2" is booked at door "WH1-D02" for ASN "ASN-2" starting in 60 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-2"
    And the gate clerk completes the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And dock-scheduling cancels appointment "appt-2"
    Then the latest move of trailer "TR-1" is REQUESTED
    And no "YardMoveCancelled" event was published

  Scenario: A door that frees up calls only a trailer that is parked
    Given ASN "ASN-2" is registered
    And appointment "appt-2" is booked at door "WH1-D01" for ASN "ASN-2" starting in 5 minutes
    And trailer "TR-1" is docked at door "WH1-D01"
    When the gate checks in visit "B" for purpose DROP with plate "XYZ9" and trailer "TR-2" (DRY_VAN, LOADED) on appointment "appt-2"
    And the gate clerk completes the move of trailer "TR-2"
    And the supervisor requests a move of trailer "TR-2" to DOOR "WH1-D02"
    And hostler "h1" claims the next move at site "WH1"
    And hostler "h1" completes the move of trailer "TR-2"
    Then trailer "TR-2" is DOCKED at DOOR "WH1-D02"
    When the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    And hostler "h2" claims the next move at site "WH1" and gets the move of trailer "TR-1"
    Then the latest move of trailer "TR-2" is COMPLETED

  Scenario: A door that frees up gives no second move to a trailer that already has one
    Given ASN "ASN-2" is registered
    And appointment "appt-2" is booked at door "WH1-D01" for ASN "ASN-2" starting in 5 minutes
    And trailer "TR-1" is docked at door "WH1-D01"
    When the gate checks in visit "B" for purpose DROP with plate "XYZ9" and trailer "TR-2" (DRY_VAN, LOADED) on appointment "appt-2"
    And the gate clerk completes the move of trailer "TR-2"
    And the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    And the supervisor requests a move of trailer "TR-2" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1" and gets the move of trailer "TR-1"
    And I send GET "/moves?trailerId=<trailer TR-2>&state=REQUESTED"
    Then the response list "items" has 1 item
    And the response field "items.0.reason" is "RELOCATE"

  Scenario: A trailer sent back to its door finds its door work as it left it
    Given trailer "TR-1" is docked at door "WH1-D02"
    When the dock operator starts door work on trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1"
    And the clock advances 301 seconds
    And the lease expiry sweep runs
    Then trailer "TR-1" is DOCKED at DOOR "WH1-D02"
    And trailer "TR-1" has door work IN_PROGRESS and load state LOADED

  Scenario: An empty trailer is not bound to the direction of the ASN on its appointment
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 120 minutes
    When the gate checks in visit "A" for purpose DROP with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, EMPTY) on appointment "appt-1"
    And the gate clerk completes the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D03"
    Then the response status is 201

  Scenario: A spot of another site is not a destination for the trailer
    Given a yard site "WH2" with spots "WH2-Y01" and doors "WH2-D01:Both"
    And the gate works at site "WH1"
    And trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH2-Y01"
    Then the response status is 422
    And the problem is "unknown-location"
