# Changelog

## [0.2.0](https://github.com/newstack-cloud/celerity-go-sdk/compare/resources/aws/v0.1.0...resources/aws/v0.2.0) (2026-10-08)


### Features

* **core:** add data store resource abstraction and core resource helpers ([db8e457](https://github.com/newstack-cloud/celerity-go-sdk/commit/db8e457ecdfdab1a4e64f8749f718f979034ad5d))
* **resources-aws:** add aws implementation of the queue resource ([660218d](https://github.com/newstack-cloud/celerity-go-sdk/commit/660218d9f3b5c9777f68adb385da952bf3982271))
* **resources-aws:** add aws implementation of the topic resource ([f94466c](https://github.com/newstack-cloud/celerity-go-sdk/commit/f94466c5a4a994fedd7214a141d10b325bce72de))
* **resources-aws:** add custom tracing middleware for aws sdk clients ([fe15eb9](https://github.com/newstack-cloud/celerity-go-sdk/commit/fe15eb99651ce1e40bcda59ae71237937e04923a))
* **resources-aws:** add dynamodb implementation of the data store resource ([d3f9c7d](https://github.com/newstack-cloud/celerity-go-sdk/commit/d3f9c7d56cd4f0f2d449c4643f71234165c0cf09))
* **resources-aws:** add elasticache auth for redis caches ([3dc2eb9](https://github.com/newstack-cloud/celerity-go-sdk/commit/3dc2eb9801a715d5a113749751031d38429ad7fc))
* **resources-aws:** add provider wiring, registry and session/client management ([3de2a8e](https://github.com/newstack-cloud/celerity-go-sdk/commit/3de2a8e018a3a50bab053ef12fe3d3931554f420))
* **resources-aws:** add rds sql resource implementation ([738061b](https://github.com/newstack-cloud/celerity-go-sdk/commit/738061b40cf320b8fc52ac545f9ea878315395ea))
* **resources-aws:** add region-scoped clients for the dynamodb data store implementation ([bf5fd64](https://github.com/newstack-cloud/celerity-go-sdk/commit/bf5fd64a83efbf5f49ee7e82f16d3d2ef559f7b8))
* **resources-aws:** add s3 implementation of bucket resource ([b1de705](https://github.com/newstack-cloud/celerity-go-sdk/commit/b1de7054b25a7493b24aabeb6a08be0859cde542))
* **resources-aws:** update rds db creds/config loading to use shared reader ([de7e6d0](https://github.com/newstack-cloud/celerity-go-sdk/commit/de7e6d04052073700a48de512a1f8c1c4ad6717c))
* **resources-aws:** use instrumented sqldb to open db pool for rds ([877510d](https://github.com/newstack-cloud/celerity-go-sdk/commit/877510da6524065d240743953e3eb02bcb493cff))
