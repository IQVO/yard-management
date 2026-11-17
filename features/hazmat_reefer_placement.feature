@bdd
Feature: Hazmat and reefer placement
  A trailer that carries hazardous materials is parked on a spot set aside for them, and a refrigerated trailer on a spot
  with a plug, where the site tells the yard which spots those are (the facility-layout spotKind). The yard refuses a
  placement the classification forbids instead of recording it afterwards. Doors are exempt. A trailer that declares
  nothing, and a spot that was published with no kind, are judged exactly as before. Which hazard classes may not stand on
  adjacent spots (49 CFR 177.848) is not enforced here.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03,WH1-Y04,WH1-Y05" and doors "WH1-D01:Both,WH1-D02:Both"
    And spot "WH1-Y01" of site "WH1" is of kind PARKING
    And spot "WH1-Y03" of site "WH1" is of kind REEFER_PLUG
    And spot "WH1-Y04" of site "WH1" is of kind HAZMAT

  Scenario: A hazmat trailer is sent to the HAZMAT spot at the gate and the classification is published
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","hazmatClass":"3","unNumber":"UN1203"}]}
    Then the response status is 201
    And the response field "trailers.0.hazmatClass" is "3"
    And the response field "trailers.0.unNumber" is "UN1203"
    And the response field "trailers.0.requiresTemperatureControl" is false
    And the latest move of trailer "TR-1" goes to SPOT "WH1-Y04"
    And the latest "VisitCheckedIn" event has field "trailers.0.hazmat_class" "3"
    And the latest "VisitCheckedIn" event has field "trailers.0.un_number" "UN1203"
    And the latest "VisitCheckedIn" event has field "trailers.0.requires_temperature_control" false
    When the gate clerk completes the move of trailer "TR-1"
    Then trailer "TR-1" is PARKED at SPOT "WH1-Y04"

  Scenario: A second hazmat trailer finds the HAZMAT spot taken and is told there is no free spot
    Given trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"hazmatClass":"3"}
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1"
    And hostler "h1" completes the move of trailer "TR-1"
    And the gate checks in visit "B" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"XYZ9","purpose":"DROP","trailers":[{"unitNumber":"TR-2","kind":"DRY_VAN","loadState":"LOADED","hazmatClass":"8"}]}
    Then the response status is 409
    And the problem is "no-free-spot"
    And the event "VisitCheckedIn" was published 1 time

  Scenario: A hazmat trailer is refused any other kind of spot and accepted on a HAZMAT spot
    Given trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"hazmatClass":"2.1","unNumber":"UN1075"}
    Then the response status is 200
    And the response field "hazmatClass" is "2.1"
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y02"
    Then the response status is 409
    And the problem is "placement-rule-violated"
    And the response field "detail" contains "hazmat class 2.1 may only be placed on a HAZMAT spot"
    And the response field "detail" contains "spot WH1-Y02 is not classified"
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    Then the response status is 409
    And the problem is "placement-rule-violated"
    And the response field "detail" contains "a REEFER_PLUG spot"
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    Then the response status is 201
    And the latest move of trailer "TR-1" goes to SPOT "WH1-Y04"

  Scenario: The placement is judged again when the hostler completes the move somewhere else
    Given trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"hazmatClass":"6.1"}
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    And hostler "h1" claims the next move at site "WH1"
    And hostler "h1" completes the move of trailer "TR-1" at SPOT "WH1-Y02" noting "the HAZMAT spot was blocked"
    Then the response status is 409
    And the problem is "placement-rule-violated"
    And trailer "TR-1" is IN_MOVE

  Scenario: A hazmat trailer may go to a dock door
    Given trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"hazmatClass":"3","unNumber":"UN1203"}
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D02"
    And hostler "h1" claims the next move at site "WH1"
    And hostler "h1" completes the move of trailer "TR-1"
    Then trailer "TR-1" is DOCKED at DOOR "WH1-D02"
    And the latest "TrailerDocked" event has field "hazmat_class" "3"
    And the latest "TrailerDocked" event has field "un_number" "UN1203"
    And the latest "TrailerDocked" event has field "requires_temperature_control" false

  Scenario: A site with no HAZMAT spot refuses a hazmat trailer unless the policy allows unclassified spots
    Given a yard site "WH2" with spots "WH2-Y01,WH2-Y02" and doors "WH2-D01:Both"
    When the gate checks in visit "A" with the body {"siteId":"WH2","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","hazmatClass":"3"}]}
    Then the response status is 409
    And the problem is "placement-rule-violated"
    And the response field "detail" contains "the site has none"
    And the response field "detail" contains "allowHazmatOnUnclassifiedSpots"
    And no "VisitCheckedIn" event was published
    Given the yard policy of site "WH2" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"allowHazmatOnUnclassifiedSpots":true}
    When the gate checks in visit "A" with the body {"siteId":"WH2","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","hazmatClass":"3"}]}
    Then the response status is 201
    And the latest move of trailer "TR-1" goes to SPOT "WH2-Y01"
    When I send GET "/sites/WH2/yard-policy"
    Then the response field "allowHazmatOnUnclassifiedSpots" is true

  Scenario: The policy admits only spots with no kind, and never where the site has a HAZMAT spot
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"allowHazmatOnUnclassifiedSpots":true}
    And trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"hazmatClass":"3"}
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y05"
    Then the response status is 409
    And the problem is "placement-rule-violated"
    And the response field "detail" contains "HAZMAT spot"

  Scenario: A site whose spots are all unclassified and whose policy allows it takes a hazmat trailer on a spot with no kind
    Given a yard site "WH2" with spots "WH2-Y01,WH2-Y02" and doors "WH2-D01:Both"
    And spot "WH2-Y01" of site "WH2" is of kind PARKING
    And the yard policy of site "WH2" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"allowHazmatOnUnclassifiedSpots":true}
    When the gate checks in visit "A" with the body {"siteId":"WH2","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","hazmatClass":"1.4"}]}
    Then the response status is 201
    And the latest move of trailer "TR-1" goes to SPOT "WH2-Y02"

  Scenario: A refrigerated trailer is sent to a plug and refused other spots where the site has plugs
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"REEFER","loadState":"LOADED","requiresTemperatureControl":true}]}
    Then the response status is 201
    And the response field "trailers.0.requiresTemperatureControl" is true
    And the latest move of trailer "TR-1" goes to SPOT "WH1-Y03"
    And the latest "VisitCheckedIn" event has field "trailers.0.requires_temperature_control" true
    When the gate clerk completes the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y02"
    Then the response status is 409
    And the problem is "placement-rule-violated"
    And the response field "detail" contains "requires temperature control may only be placed on a REEFER_PLUG spot"

  Scenario: A refrigerated trailer at a dock door is fine
    Given trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"requiresTemperatureControl":true}
    And the supervisor requests a move of trailer "TR-1" to DOOR "WH1-D01"
    Then the response status is 201

  Scenario: Where a site has no plug, a refrigerated trailer is parked as before
    Given a yard site "WH2" with spots "WH2-Y01,WH2-Y02" and doors "WH2-D01:Both"
    When the gate checks in visit "A" with the body {"siteId":"WH2","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"REEFER","loadState":"LOADED","requiresTemperatureControl":true}]}
    Then the response status is 201
    And the latest move of trailer "TR-1" goes to SPOT "WH2-Y01"

  Scenario: A trailer that is both hazmat and refrigerated is judged by the hazmat rule
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"REEFER","loadState":"LOADED","hazmatClass":"5.2","unNumber":"UN3101","requiresTemperatureControl":true}]}
    Then the response status is 201
    And the latest move of trailer "TR-1" goes to SPOT "WH1-Y04"

  Scenario: An ordinary trailer may stand on a HAZMAT or a REEFER_PLUG spot (not forbidden)
    Given trailer "TR-1" is parked
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y04"
    Then the response status is 201
    When hostler "h1" claims the next move at site "WH1"
    And hostler "h1" completes the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y03"
    Then the response status is 201

  Scenario: A trailer that declares nothing and a spot with no kind behave exactly as before
    Given a yard site "WH2" with spots "WH2-Y01,WH2-Y02" and doors "WH2-D01:Both"
    When the gate checks in visit "A" with the body {"siteId":"WH2","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"REEFER","loadState":"LOADED"}]}
    Then the response status is 201
    And the response field "trailers.0.hazmatClass" is null
    And the response field "trailers.0.unNumber" is null
    And the response field "trailers.0.requiresTemperatureControl" is false
    And the latest move of trailer "TR-1" goes to SPOT "WH2-Y01"
    And the latest "VisitCheckedIn" event has field "trailers.0.hazmat_class" null
    When the gate clerk completes the move of trailer "TR-1"
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH2-Y02"
    Then the response status is 201

  Scenario Outline: A hazard class is a US DOT class or division
    Given trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"hazmatClass":"<class>"}
    Then the response status is <status>

    Examples:
      | class | status |
      | 1     | 200    |
      | 1.4   | 200    |
      | 2.1   | 200    |
      | 3     | 200    |
      | 4.3   | 200    |
      | 5.2   | 200    |
      | 6.1   | 200    |
      | 7     | 200    |
      | 8     | 200    |
      | 9     | 200    |
      | 0     | 422    |
      | 10    | 422    |
      | 1.7   | 422    |
      | 2.4   | 422    |
      | 3.1   | 422    |
      | 1.4S  | 422    |
      | class | 422    |

  Scenario Outline: An invalid hazard class or UN number is refused at the gate and nothing is recorded
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","hazmatClass":"<class>","unNumber":"<un>"}]}
    Then the response status is 422
    And the problem is "<problem>"
    And no "VisitCheckedIn" event was published

    Examples:
      | class | un     | problem               |
      | 9.1   |        | invalid-hazmat-class  |
      | 3     | UN123  | invalid-un-number     |
      | 3     | un1203 | invalid-un-number     |
      | 3     | 1203   | invalid-un-number     |
      | 3     | UN12A4 | invalid-un-number     |
      |       | UN1203 | validation            |

  Scenario: The classification is replaced as a whole, and the same classification again changes nothing
    Given trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"hazmatClass":"3","unNumber":"UN1203","requiresTemperatureControl":true}
    Then the response status is 200
    And the response field "hazmatClass" is "3"
    And the response field "requiresTemperatureControl" is true
    When I send GET "/trailers/<trailer TR-1>"
    Then the response field "unNumber" is "UN1203"
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"hazmatClass":"3","unNumber":"UN1203","requiresTemperatureControl":true}
    Then the response status is 200
    And trailer "TR-1" is PARKED at SPOT "WH1-Y01"
    When I send POST "/trailers/<trailer TR-1>/classifications" with {}
    Then the response status is 200
    And the response field "hazmatClass" is null
    And the response field "unNumber" is null
    And the response field "requiresTemperatureControl" is false

  Scenario: Classifying a parked trailer does not move it, and it is judged when it is next placed
    Given trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"hazmatClass":"3"}
    Then trailer "TR-1" is PARKED at SPOT "WH1-Y01"
    When the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y05"
    Then the response status is 409
    And the problem is "placement-rule-violated"

  Scenario: A slot registered with a spot kind a later facility-layout adds is stored as unknown
    Given spot "WH1-Y05" of site "WH1" is of kind COLD_STORE
    And trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/classifications" with {"requiresTemperatureControl":true}
    And the supervisor requests a move of trailer "TR-1" to SPOT "WH1-Y05"
    Then the response status is 409
    And the problem is "placement-rule-violated"
