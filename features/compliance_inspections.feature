@bdd
Feature: Compliance inspections
  A checklist is filled in for a trailer, and where the site policy asks for it the checklist gates the next yard
  step: a trailer that has not passed its inspection is not docked, loaded or let out of the gate. A required item that
  fails holds the trailer, and the record of who checked what stays as it was, whatever the template becomes later.
  A site that requires no inspection behaves exactly as before.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03" and doors "WH1-D01:Inbound,WH1-D02:Both"

  # ---------------------------------------------------------------- templates

  Scenario: The built-in templates ship with the service
    When I send GET "/inspection-templates"
    Then the response status is 200
    And the response list "items" has 3 items
    And the response field "items.0.templateId" is "empty-container-7-point"
    And the response field "items.1.templateId" is "reefer-pre-cool-and-sanitation"
    And the response field "items.2.templateId" is "tractor-trailer-17-point"
    And the response field "items.2.builtIn" is true
    And the response field "items.2.publishedAt" is null
    When I send GET "/inspection-templates/tractor-trailer-17-point"
    Then the response status is 200
    And the response field "version" is 1
    And the response field "latestVersion" is 1
    And the response list "items" has 19 items
    And the response field "items.0.itemCode" is "bumper-tires-rims"
    And the response field "items.15.itemCode" is "refrigeration-unit"
    And the response field "items.15.allowNotApplicable" is true
    And the response field "items.17.itemCode" is "pest-free"
    And the response field "items.17.holdReason" is "QUALITY"
    When I send GET "/inspection-templates/empty-container-7-point/versions/1"
    Then the response list "items" has 10 items
    When I send GET "/inspection-templates/reefer-pre-cool-and-sanitation"
    Then the response list "items" has 6 items
    And the response field "items.1.itemCode" is "sanitary-condition-washout"

  Scenario: A built-in template is read-only
    When I send PUT "/inspection-templates/tractor-trailer-17-point/versions/2" with {"title":"Mine","items":[{"itemCode":"a","text":"A.","required":true}]}
    Then the response status is 409
    And the problem is "template-is-built-in"

  Scenario: A custom template is published once per version and never changes
    When I send PUT "/inspection-templates/yard-entry/versions/1" with {"title":"Yard entry","items":[{"itemCode":"lights","text":"Lights work.","required":true}]}
    Then the response status is 201
    And the response field "builtIn" is false
    And the response field "items.0.holdReason" is "QUALITY"
    And the response field "publishedAt" is "2026-11-05T14:00:00Z"
    When I send PUT "/inspection-templates/yard-entry/versions/1" with {"title":"Yard entry","items":[{"itemCode":"lights","text":"Lights work.","required":true}]}
    Then the response status is 200
    When I send PUT "/inspection-templates/yard-entry/versions/1" with {"title":"Yard entry","items":[{"itemCode":"lights","text":"Lights work.","required":false}]}
    Then the response status is 409
    And the problem is "template-version-immutable"
    When I send PUT "/inspection-templates/yard-entry/versions/2" with {"title":"Yard entry v2","items":[{"itemCode":"lights","text":"Lights work.","required":true},{"itemCode":"horn","text":"Horn works.","required":false}]}
    Then the response status is 201
    And the response field "latestVersion" is 2
    When I send GET "/inspection-templates/yard-entry"
    Then the response field "version" is 2
    And the response list "items" has 2 items
    When I send GET "/inspection-templates/yard-entry/versions/1"
    Then the response field "version" is 1
    And the response field "latestVersion" is 2
    And the response list "items" has 1 items
    When I send GET "/inspection-templates"
    Then the response list "items" has 4 items
    And the response field "items.3.templateId" is "yard-entry"
    And the response field "items.3.version" is 2

  Scenario Outline: A template that is not well formed is refused
    When I send PUT "/inspection-templates/<id>/versions/<version>" with <body>
    Then the response status is <status>
    And the problem is "<problem>"

    Examples:
      | id         | version | body                                                                                     | status | problem    |
      | yard-entry | 1       | {"title":"","items":[{"itemCode":"a","text":"A.","required":true}]}                       | 422    | validation |
      | yard-entry | 1       | {"title":"T","items":[]}                                                                  | 422    | validation |
      | yard-entry | 1       | {"title":"T","items":[{"itemCode":"A B","text":"A.","required":true}]}                    | 422    | validation |
      | yard-entry | 1       | {"title":"T","items":[{"itemCode":"a","text":"A."},{"itemCode":"a","text":"B."}]}        | 422    | validation |
      | yard-entry | 1       | {"title":"T","items":[{"itemCode":"a","text":"A.","holdReason":"BORING"}]}               | 422    | validation |
      | yard-entry | 0       | {"title":"T","items":[{"itemCode":"a","text":"A."}]}                                      | 422    | validation |
      | Yard_Entry | 1       | {"title":"T","items":[{"itemCode":"a","text":"A."}]}                                      | 422    | validation |
      | yard-entry | 1       | {"title":"T","items":[{"itemCode":"a","text":"A."}],"extra":true}                         | 400    | malformed-request-body |

  Scenario: An unknown template is not found
    When I send GET "/inspection-templates/nope"
    Then the response status is 404
    And the problem is "template-not-found"
    When I send GET "/inspection-templates/yard-entry/versions/9"
    Then the response status is 404
    And the problem is "template-not-found"

  # -------------------------------------------------------------- inspections

  Scenario: An inspection is opened with every item pending, from the latest version of the template
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    Then the response status is 201
    And the response header "Location" is "/inspections/insp-000001"
    And the response field "inspectionId" is "insp-000001"
    And the response field "status" is "OPEN"
    And the response field "gate" is "PRE_DOCK"
    And the response field "templateId" is "yard-entry"
    And the response field "templateVersion" is 1
    And the response field "visitId" is "visit-000001"
    And the response field "performerRef" is "emp-7"
    And the response field "conditionGrade" is null
    And the response field "completedAt" is null
    And the response list "items" has 4 items
    And the response field "items.0.itemCode" is "lights"
    And the response field "items.0.result" is null
    And the response field "items.0.evidenceRefs" is []
    And the response field "items.2.holdReason" is "SEAL_BROKEN"
    And the response field "version" is 1
    And no "InspectionCompleted" event was published

  Scenario: An inspection that passes
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    And the inspector records item "lights" of inspection "A" as PASS
    Then the response status is 200
    And the response field "items.0.result" is "PASS"
    And the response field "version" is 2
    When the inspector records item "reefer-unit" of inspection "A" as NOT_APPLICABLE
    And the inspector records item "seal-intact" of inspection "A" as PASS
    And the inspector completes inspection "A" with condition CLEAN
    Then the response status is 200
    And the response field "status" is "PASSED"
    And the response field "conditionGrade" is "CLEAN"
    And the response field "completedAt" is "2026-11-05T14:00:00Z"
    And the event "InspectionCompleted" was published 1 time
    And no "InspectionFailed" event was published
    And no "TrailerHeld" event was published
    When I send GET "/inspections/<inspection A>"
    Then the response ETag is version 5
    And the latest "InspectionCompleted" event has field "outcome" "PASSED"
    And the latest "InspectionCompleted" event has field "template_version" 1
    And the latest "InspectionCompleted" event has field "gate" "PRE_DOCK"
    And the latest "InspectionCompleted" event has field "condition_grade" "CLEAN"
    And the latest "InspectionCompleted" event has field "failed_item_codes" []
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"

  Scenario: A required item that is still pending blocks the completion
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    And the inspector records item "lights" of inspection "A" as PASS
    And the inspector completes inspection "A"
    Then the response status is 409
    And the problem is "required-item-pending"
    And inspection "A" has status OPEN
    And no "InspectionCompleted" event was published
    When the inspector records item "reefer-unit" of inspection "A" as PASS
    And the inspector records item "seal-intact" of inspection "A" as PASS
    And the inspector completes inspection "A"
    Then the response status is 200
    And the response field "status" is "PASSED"

  Scenario: An optional item may stay pending
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    And the inspector records item "lights" of inspection "A" as PASS
    And the inspector records item "reefer-unit" of inspection "A" as PASS
    And the inspector records item "seal-intact" of inspection "A" as PASS
    And the inspector completes inspection "A"
    Then the response field "status" is "PASSED"
    And the response field "items.3.result" is null

  Scenario: A failed required item fails the inspection and holds the trailer
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_LOAD inspection "A" of template "yard-entry" on trailer "TR-1"
    And the inspector records item "lights" of inspection "A" as PASS
    And the inspector records item "reefer-unit" of inspection "A" as NOT_APPLICABLE
    And the inspector records item "seal-intact" of inspection "A" as FAIL noting "bolt cut at the hinge" with evidence "photo:abc,doc:42"
    And the inspector completes inspection "A"
    Then the response status is 200
    And the response field "status" is "FAILED"
    And trailer "TR-1" is on hold SEAL_BROKEN
    And the event "InspectionCompleted" was published 1 time
    And the event "InspectionFailed" was published 1 time
    And the event "TrailerHeld" was published 1 time
    And the published events include, in order, "InspectionCompleted,InspectionFailed,TrailerHeld"
    And the latest "InspectionFailed" event has field "hold_placed" true
    And the latest "InspectionFailed" event has field "gate" "PRE_LOAD"
    And the latest "InspectionFailed" event has field "failed_items.0.item_code" "seal-intact"
    And the latest "InspectionFailed" event has field "failed_items.0.note" "bolt cut at the hinge"
    And the latest "InspectionFailed" event has field "failed_items.0.required" true
    And the latest "InspectionCompleted" event has field "outcome" "FAILED"
    And the latest "InspectionCompleted" event has field "failed_item_codes.0" "seal-intact"
    When I send GET "/inspections/<inspection A>"
    Then the response field "items.2.evidenceRefs.1" is "doc:42"
    When the supervisor releases the hold on trailer "TR-1"
    Then the response status is 200
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"

  Scenario: The hold takes the reason of the most severe failed item, and a trailer already held is left as it is
    Given trailer "TR-1" is parked
    And the supervisor places a DAMAGED hold on trailer "TR-1"
    When the inspector opens a ON_DEMAND inspection "A" of template "empty-container-7-point" on trailer "TR-1"
    And the inspector records every item of inspection "A" as PASS
    And the inspector records item "pest-free" of inspection "A" as FAIL
    And the inspector completes inspection "A"
    Then the response field "status" is "FAILED"
    And the latest "InspectionFailed" event has field "hold_placed" false
    And trailer "TR-1" is on hold DAMAGED
    And the event "TrailerHeld" was published 1 time

  Scenario: A failed required item of a security check holds the trailer for SECURITY
    Given trailer "TR-1" is parked
    When the inspector opens a ON_DEMAND inspection "A" of template "tractor-trailer-17-point" on trailer "TR-1"
    And the inspector records every item of inspection "A" as PASS
    And the inspector records item "pest-free" of inspection "A" as FAIL
    And the inspector records item "fifth-wheel" of inspection "A" as FAIL
    And the inspector completes inspection "A"
    Then the response field "status" is "FAILED"
    And trailer "TR-1" is on hold SECURITY
    And the latest "InspectionFailed" event has field "failed_items.0.item_code" "fifth-wheel"
    And the latest "InspectionFailed" event has field "failed_items.1.item_code" "pest-free"

  Scenario: Not applicable is allowed only where the template says so
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    And the inspector records item "lights" of inspection "A" as NOT_APPLICABLE
    Then the response status is 422
    And the problem is "not-applicable-not-allowed"
    When the inspector records item "reefer-unit" of inspection "A" as NOT_APPLICABLE
    Then the response status is 200
    And the response field "items.1.result" is "NOT_APPLICABLE"

  Scenario Outline: A result that cannot be recorded is refused
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    And I send PUT "/inspections/<inspection A>/items/<item>" with <body>
    Then the response status is <status>
    And the problem is "<problem>"

    Examples:
      | item   | body                                                      | status | problem            |
      | wipers | {"result":"PASS"}                                         | 422    | unknown-item       |
      | lights | {"result":"MAYBE"}                                        | 422    | validation         |
      | lights | {"result":"PASS","evidenceRefs":["a","a"]}                | 422    | validation         |
      | lights | {"result":"PASS","unknown":1}                             | 400    | malformed-request-body |

  Scenario: The record is final once the inspection is completed
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    And the inspector records every item of inspection "A" as PASS
    And the inspector completes inspection "A"
    And the inspector records item "lights" of inspection "A" as FAIL
    Then the response status is 409
    And the problem is "inspection-closed"
    When the inspector completes inspection "A"
    Then the response status is 409
    And the problem is "inspection-closed"
    And inspection "A" has status PASSED
    And the event "InspectionCompleted" was published 1 time

  Scenario: An inspection keeps the template version it was opened from
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    And I send PUT "/inspection-templates/yard-entry/versions/2" with {"title":"Yard entry v2","items":[{"itemCode":"horn","text":"Horn works.","required":true}]}
    Then the response status is 201
    When I send GET "/inspections/<inspection A>"
    Then the response field "templateVersion" is 1
    And the response list "items" has 4 items
    And the response field "items.0.itemCode" is "lights"
    When I send POST "/trailers/<trailer TR-1>/inspections" with {"templateId":"yard-entry","gate":"ON_DEMAND"}
    Then the response field "templateVersion" is 2
    And the response list "items" has 1 items
    When I send POST "/trailers/<trailer TR-1>/inspections" with {"templateId":"yard-entry","templateVersion":1,"gate":"PRE_LOAD"}
    Then the response status is 201
    And the response field "templateVersion" is 1
    And the response list "items" has 4 items

  Scenario: Only one inspection per trailer, template and gate is open at a time
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    And the inspector opens a PRE_DOCK inspection "B" of template "yard-entry" on trailer "TR-1"
    Then the response status is 409
    And the problem is "inspection-already-open"
    When the inspector opens a PRE_LOAD inspection "C" of template "yard-entry" on trailer "TR-1"
    Then the response status is 201

  Scenario Outline: An inspection that cannot be opened
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/inspections" with <body>
    Then the response status is <status>
    And the problem is "<problem>"

    Examples:
      | body                                                                  | status | problem          |
      | {"templateId":"nope","gate":"PRE_DOCK"}                               | 422    | unknown-template |
      | {"templateId":"yard-entry","templateVersion":9,"gate":"PRE_DOCK"}     | 422    | unknown-template |
      | {"templateId":"yard-entry","gate":"SOMETIME"}                         | 422    | validation       |
      | {"templateId":"yard-entry"}                                           | 422    | validation       |
      | {"templateId":"yard-entry","gate":"PRE_DOCK","visitId":"visit-nope"}  | 404    | visit-not-found  |

  Scenario: An inspection of a trailer that is not there is refused
    When I send POST "/trailers/trl-nope/inspections" with {"templateId":"yard-entry","gate":"PRE_DOCK"}
    Then the response status is 404
    And the problem is "trailer-not-found"
    When I send GET "/inspections/insp-nope"
    Then the response status is 404
    And the problem is "inspection-not-found"
    When I send GET "/trailers/trl-nope/inspections"
    Then the response status is 404
    And the problem is "trailer-not-found"

  Scenario: A departed trailer is not inspected
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    When visit "TR-1" checks out taking trailers "TR-1"
    And I send POST "/trailers/<trailer TR-1>/inspections" with {"templateId":"yard-entry","gate":"ON_DEMAND"}
    Then the response status is 409
    And the problem is "illegal-transition"

  Scenario: A trailer's inspections are listed, newest first, and a re-inspection can follow a failure
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    And trailer "TR-1" has a FAILED ON_DEMAND inspection of template "yard-entry"
    When the supervisor releases the hold on trailer "TR-1"
    And the inspector opens a ON_DEMAND inspection "R" of template "yard-entry" on trailer "TR-1"
    Then the response status is 201
    When I send GET "/trailers/<trailer TR-1>/inspections"
    Then the response status is 200
    And the response list "items" has 2 items
    And the response field "items.0.status" is "OPEN"
    And the response field "items.1.status" is "FAILED"
    When I send GET "/trailers/<trailer TR-1>/inspections?limit=1"
    Then the response list "items" has 1 item
    When I send GET "/trailers/<trailer TR-1>/inspections?state=OPEN"
    Then the response status is 400
    And the problem is "unknown-query-parameter"

  # ------------------------------------------------------------------- gating

  Scenario: Where no inspection is required the yard behaves as before
    Given trailer "TR-1" is parked
    When I send GET "/sites/WH1/yard-policy"
    Then the response field "requiredInspections" is []
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 201
    When hostler "h1" claims the next move at site "WH1"
    And hostler "h1" completes the move of trailer "TR-1"
    Then trailer "TR-1" is DOCKED at DOOR "WH1-D02"
    When the dock operator starts door work on trailer "TR-1"
    Then the response status is 200

  Scenario: A trailer is not sent to a door until it has passed the pre-dock inspection
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"yard-entry"}]}
    When I send GET "/sites/WH1/yard-policy"
    Then the response field "requiredInspections.0.gate" is "PRE_DOCK"
    And the response field "requiredInspections.0.templateId" is "yard-entry"
    And the response field "requiredInspections.0.appliesTo.purposes" is []
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "inspection-required"
    And trailer "TR-1" has no open move
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    Then the response status is 201
    When the supervisor cancels the move of trailer "TR-1"
    And the inspector opens a PRE_DOCK inspection "A" of template "yard-entry" on trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "inspection-required"
    When the inspector records every item of inspection "A" as PASS
    And the inspector completes inspection "A"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 201
    When hostler "h1" claims the next move at site "WH1"
    And hostler "h1" completes the move of trailer "TR-1"
    Then trailer "TR-1" is DOCKED at DOOR "WH1-D02"

  Scenario: A trailer that failed the pre-dock inspection stays out of the door until a re-inspection passes
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"yard-entry"}]}
    And trailer "TR-1" has a FAILED PRE_DOCK inspection of template "yard-entry"
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "trailer-on-hold"
    When the supervisor releases the hold on trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "inspection-failed"
    When the inspector opens a PRE_DOCK inspection "B" of template "yard-entry" on trailer "TR-1"
    And the inspector records every item of inspection "B" as PASS
    And the inspector completes inspection "B"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 201

  Scenario: A pass at another gate, or on demand, does not open the pre-dock gate
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"yard-entry"}]}
    And trailer "TR-1" has a PASSED ON_DEMAND inspection of template "yard-entry"
    And trailer "TR-1" has a PASSED PRE_LOAD inspection of template "yard-entry"
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "inspection-required"

  Scenario: The rule can be narrowed to the trailer kinds and purposes it is about
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"yard-entry","appliesTo":{"trailerKinds":["REEFER"]}}]}
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 201
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"yard-entry","appliesTo":{"purposes":["LIVE_UNLOAD","PICKUP"]}}]}
    When the supervisor cancels the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 201
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"yard-entry","appliesTo":{"purposes":["DROP"],"trailerKinds":["DRY_VAN"]}}]}
    When the supervisor cancels the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 409
    And the problem is "inspection-required"

  Scenario: A trailer that arrives with an appointment is parked, not docked, while the pre-dock inspection is missing
    Given the custom template "yard-entry" version 1 is published
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"yard-entry"}]}
    And ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    Then the response status is 201
    And the response field "moves.0.reason" is "TO_SPOT"
    And the response field "moves.0.to.kind" is "SPOT"

  Scenario: Loading does not start until the pre-load inspection has passed
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" of a LIVE_LOAD visit is docked at door "WH1-D02"
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_LOAD","templateId":"yard-entry"}]}
    When the dock operator starts door work on trailer "TR-1"
    Then the response status is 409
    And the problem is "inspection-required"
    And trailer "TR-1" has door work WAITING and load state LOADED
    And no "DoorWorkStarted" event was published
    When the inspector opens a PRE_LOAD inspection "L" of template "yard-entry" on trailer "TR-1"
    And the inspector records every item of inspection "L" as PASS
    And the inspector completes inspection "L"
    And the dock operator starts door work on trailer "TR-1"
    Then the response status is 200
    And trailer "TR-1" has door work IN_PROGRESS and load state LOADED

  Scenario: A failed pre-load inspection holds the trailer at its door
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" of a LIVE_LOAD visit is docked at door "WH1-D02"
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_LOAD","templateId":"yard-entry"}]}
    And trailer "TR-1" has a FAILED PRE_LOAD inspection of template "yard-entry"
    Then trailer "TR-1" is on hold QUALITY
    When the dock operator starts door work on trailer "TR-1"
    Then the response status is 409
    And the problem is "trailer-on-hold"
    When the supervisor releases the hold on trailer "TR-1"
    And the dock operator starts door work on trailer "TR-1"
    Then the response status is 409
    And the problem is "inspection-failed"

  Scenario: A trailer that is being unloaded is not asked for a pre-load inspection
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is docked at door "WH1-D02"
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_LOAD","templateId":"yard-entry"}]}
    When the dock operator starts door work on trailer "TR-1"
    Then the response status is 200

  Scenario: Closing the receipt completes an unload even where inspections are required
    Given the custom template "yard-entry" version 1 is published
    And ASN "ASN-1" is registered
    And appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes
    And receipt "rcpt-1" is opened for ASN "ASN-1" on appointment "appt-1" at door "WH1-D01"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1"
    And the gate clerk completes the move of trailer "TR-1"
    And the dock operator starts door work on trailer "TR-1"
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"yard-entry"},{"gate":"PRE_LOAD","templateId":"yard-entry"},{"gate":"PRE_DEPARTURE","templateId":"yard-entry"}]}
    And inbound-receiving closes receipt "rcpt-1" for ASN "ASN-1"
    Then trailer "TR-1" has door work DONE and load state EMPTY

  Scenario: A trailer does not leave the site until it has passed the pre-departure inspection
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DEPARTURE","templateId":"yard-entry"}]}
    When I send POST "/trailers/<trailer TR-1>/seals" with {"state":"INTACT","sealNumber":"SL-5"}
    And the gate checks in visit "P" for purpose PICKUP with plate "PU1"
    And visit "P" checks out taking trailers "TR-1"
    Then the response status is 409
    And the problem is "inspection-required"
    And visit "P" is CHECKED_IN
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"
    And no "TrailerDeparted" event was published
    And no "VisitCheckedOut" event was published
    When the inspector opens a PRE_DEPARTURE inspection "D" of template "yard-entry" on trailer "TR-1"
    And the inspector records every item of inspection "D" as PASS
    And the inspector completes inspection "D"
    And visit "P" checks out taking trailers "TR-1"
    Then the response status is 200
    And trailer "TR-1" is not on site
    And the event "TrailerDeparted" was published

  Scenario: A drop leaves without the pre-departure inspection because it takes no trailer
    Given the custom template "yard-entry" version 1 is published
    And trailer "TR-1" is parked
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DEPARTURE","templateId":"yard-entry"}]}
    When visit "TR-1" checks out taking no trailers
    Then the response status is 200

  Scenario: A policy that requires a template nobody published is refused
    When I send PUT "/sites/WH1/yard-policy" with {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"ghost"}]}
    Then the response status is 422
    And the problem is "unknown-template"

  Scenario Outline: A required inspection that is not well formed is refused
    When I send PUT "/sites/WH1/yard-policy" with {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[<rule>]}
    Then the response status is 422
    And the problem is "validation"

    Examples:
      | rule                                                                                                  |
      | {"gate":"ON_DEMAND","templateId":"tractor-trailer-17-point"}                                          |
      | {"gate":"SOMETIME","templateId":"tractor-trailer-17-point"}                                           |
      | {"gate":"PRE_DOCK","templateId":""}                                                                   |
      | {"gate":"PRE_DOCK","templateId":"tractor-trailer-17-point","appliesTo":{"purposes":["JOYRIDE"]}}      |
      | {"gate":"PRE_DOCK","templateId":"tractor-trailer-17-point","appliesTo":{"trailerKinds":["BOAT"]}}     |

  Scenario: Replacing the policy without requiredInspections turns the rules off again
    Given trailer "TR-1" is parked
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"tractor-trailer-17-point"}]}
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the problem is "inspection-required"
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300}
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 201

  Scenario: The built-in 17-point inspection gates a dock when the policy asks for it
    Given trailer "TR-1" is parked
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"requiredInspections":[{"gate":"PRE_DOCK","templateId":"tractor-trailer-17-point"}]}
    And trailer "TR-1" has a PASSED PRE_DOCK inspection of template "tractor-trailer-17-point"
    When the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    Then the response status is 201
