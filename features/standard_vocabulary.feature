@bdd
Feature: Published facts carry their standard meaning
  Partners read standards, not this context's private event names: the ANSI X12 214 shipment status code and the GS1
  EPCIS Core Business Vocabulary (CBV) business step and disposition (ADR 0018). A fact carries them as ADDITIVE optional
  data fields (x12_status, cbv_biz_step, cbv_disposition), only where the standard's own definition says what the fact
  says, and omits them (never null) everywhere else, so every other fact is byte-for-byte what it was. The scenarios read the
  wire data the outbox row would carry: the very function the Postgres publisher uses.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03" and doors "WH1-D01:Inbound,WH1-D02:Both"

  Scenario: The gate in and the gate out are In-Gate and Out-Gate
    When the gate checks in visit "A" for purpose DROP with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED)
    Then the latest "VisitCheckedIn" event on the wire has "x12_status" "I1"
    And the latest "VisitCheckedIn" event on the wire has "cbv_biz_step" "urn:epcglobal:cbv:bizstep:arriving"
    And the latest "VisitCheckedIn" event on the wire has no "cbv_disposition"
    When the gate clerk completes the move of trailer "TR-1"
    And visit "A" checks out taking no trailers
    Then the latest "VisitCheckedOut" event on the wire has "x12_status" "OA"
    And the latest "VisitCheckedOut" event on the wire has "cbv_biz_step" "urn:epcglobal:cbv:bizstep:departing"
    And the latest "VisitCheckedOut" event on the wire has no "cbv_disposition"

  Scenario: A trailer that leaves the site departs through the Out-Gate
    Given trailer "TR-1" is parked
    When visit "TR-1" checks out taking trailers "TR-1"
    Then the latest "TrailerDeparted" event on the wire has "x12_status" "OA"
    And the latest "TrailerDeparted" event on the wire has "cbv_biz_step" "urn:epcglobal:cbv:bizstep:departing"
    And the latest "TrailerDeparted" event on the wire has no "cbv_disposition"

  Scenario: The facts the standards have no honest word for are exactly the legacy payload
    Given ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes with expected seal "SL-1"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1" sealed "SL-1"
    And the gate clerk completes the move of trailer "TR-1"
    And the dock operator secures trailer "TR-1" with RESTRAINT
    And the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    And the dock operator releases the securing of trailer "TR-1"
    And visit "A" checks out taking trailers "TR-1"
    Then the latest "TrailerDocked" event on the wire is exactly its legacy payload
    And the latest "TrailerSecured" event on the wire is exactly its legacy payload
    And the latest "DoorWorkStarted" event on the wire is exactly its legacy payload
    And the latest "DoorWorkCompleted" event on the wire is exactly its legacy payload
    And the latest "TrailerSecuringReleased" event on the wire is exactly its legacy payload
    And the latest "TrailerUndocked" event on the wire is exactly its legacy payload
    And the latest "YardMoveRequested" event on the wire is exactly its legacy payload

  Scenario: A visit turned away at the gate was never In-Gate
    When the gate rejects a visit with plate "NOPE1" and driver "drv-9" because SITE_REQUEST
    Then the response status is 201
    And the latest "VisitRejected" event on the wire is exactly its legacy payload

  Scenario: A passed inspection is conformant and a failed one non-conformant
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    And the inspector records item "lights" of inspection "A" as PASS
    And the inspector records item "reefer-unit" of inspection "A" as NOT_APPLICABLE
    And the inspector records item "seal-intact" of inspection "A" as PASS
    And the inspector completes inspection "A" with condition CLEAN
    Then the latest "InspectionCompleted" event on the wire has "cbv_biz_step" "urn:epcglobal:cbv:bizstep:inspecting"
    And the latest "InspectionCompleted" event on the wire has "cbv_disposition" "urn:epcglobal:cbv:disp:conformant"
    And the latest "InspectionCompleted" event on the wire has no "x12_status"
    And no "InspectionFailed" event was published
    When the inspector opens a PRE_LOAD inspection "B" of template "yard-entry" on trailer "TR-1"
    And the inspector records item "lights" of inspection "B" as PASS
    And the inspector records item "reefer-unit" of inspection "B" as NOT_APPLICABLE
    And the inspector records item "seal-intact" of inspection "B" as FAIL noting "bolt cut at the hinge" with evidence "photo:abc"
    And the inspector completes inspection "B"
    Then the latest "InspectionCompleted" event on the wire has "cbv_disposition" "urn:epcglobal:cbv:disp:non_conformant"
    And the latest "InspectionFailed" event on the wire has "cbv_biz_step" "urn:epcglobal:cbv:bizstep:inspecting"
    And the latest "InspectionFailed" event on the wire has "cbv_disposition" "urn:epcglobal:cbv:disp:non_conformant"
    And the latest "InspectionFailed" event on the wire has no "x12_status"

  Scenario: The hold an inspection places for a broken seal says the seal is broken
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_LOAD inspection "A" of template "yard-entry" on trailer "TR-1"
    And the inspector records item "lights" of inspection "A" as PASS
    And the inspector records item "reefer-unit" of inspection "A" as NOT_APPLICABLE
    And the inspector records item "seal-intact" of inspection "A" as FAIL noting "bolt cut at the hinge" with evidence "photo:abc"
    And the inspector completes inspection "A"
    Then trailer "TR-1" is on hold SEAL_BROKEN
    And the latest "TrailerHeld" event on the wire has "cbv_disposition" "urn:epcglobal:cbv:disp:container_open"
    And the latest "TrailerHeld" event on the wire has no "x12_status"
    And the latest "TrailerHeld" event on the wire has no "cbv_biz_step"

  Scenario Outline: A hold is mapped to a disposition only where the reason is the disposition
    Given trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/holds" with {"reason":"<reason>"}
    Then the response status is 200
    And the latest "TrailerHeld" event on the wire <expectation>

    Examples:
      | reason      | expectation                                                              |
      | DAMAGED     | has "cbv_disposition" "urn:epcglobal:cbv:disp:damaged"                   |
      | SEAL_BROKEN | has "cbv_disposition" "urn:epcglobal:cbv:disp:container_open"            |
      | NOT_FOUND   | is exactly its legacy payload                                            |
      | QUALITY     | is exactly its legacy payload                                            |
      | SECURITY    | is exactly its legacy payload                                            |

  Scenario: A consumer written before the standard fields decodes the new payload and ignores them
    When the gate checks in visit "A" for purpose DROP with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED)
    Then a consumer written before the standard fields decodes the latest "VisitCheckedIn" event on the wire and ignores them
