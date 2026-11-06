# yard-management

Yard Management (WMS-tier Supporting bounded context of the yard domain): Owns everything physically on site outside the four walls: who passed the gate (visits), which trailer is where (spot, door or an open move), its load, seal and hold state, the moves hostlers and visiting drivers make, door work at the door, and yard checks. The source of dwell, door occupancy and yard truck productivity facts.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

Status: under construction. The design is proposed in the yard instance of
[warehouse-docs](https://iqvo.github.io/warehouse-docs/yard/contexts/yard-management/); `yard/model/model.yaml` there is the single source of truth.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.warehouse.wms.yard-management.<entity>.<Event>`), transactional outbox.

See `docs/adr/` for decisions.
