# Changelog

## [0.6.2](https://github.com/InterWorks/typesense-operator/compare/typesense-operator-0.6.1...typesense-operator-0.6.2) (2026-09-01)


### Bug Fixes

* **ci:** host helm chart packages directly on gh-pages ([#35](https://github.com/InterWorks/typesense-operator/issues/35)) ([38716cd](https://github.com/InterWorks/typesense-operator/commit/38716cddfc374299a1a72788a2fb761bca30be9b))

## [0.6.1](https://github.com/InterWorks/typesense-operator/compare/typesense-operator-0.6.0...typesense-operator-0.6.1) (2026-09-01)


### Bug Fixes

* **docker:** bump builder image to go 1.27 ([#32](https://github.com/InterWorks/typesense-operator/issues/32)) ([01b92c2](https://github.com/InterWorks/typesense-operator/commit/01b92c271c68d8a8e73f138b81af6162077729c6))

## [0.6.0](https://github.com/InterWorks/typesense-operator/compare/typesense-operator-0.5.0...typesense-operator-0.6.0) (2026-09-01)


### Features

* add TypesenseApiKey CRD and reconciler ([3fc31d5](https://github.com/InterWorks/typesense-operator/commit/3fc31d56f747307aafb77e795df33ef4dbef3df0))
* add TypesenseApiKey CRD for declarative Typesense API key management ([2aa008f](https://github.com/InterWorks/typesense-operator/commit/2aa008f084eb432d4e8e6a12bde976dd2f10681e))
* configurable healthcheck timeout ([#161](https://github.com/InterWorks/typesense-operator/issues/161)) ([b926a98](https://github.com/InterWorks/typesense-operator/commit/b926a986faca697f0db944e301b9b52f3ed97b60))
* configurable pod annotations ([#168](https://github.com/InterWorks/typesense-operator/issues/168)) ([7f318af](https://github.com/InterWorks/typesense-operator/commit/7f318af6ed8b9e79f0b49648a0d41166522ab2e2))
* **ingres:** allow to set the tls secret name and make CLusterIssuer optional ([#99](https://github.com/InterWorks/typesense-operator/issues/99)) ([#100](https://github.com/InterWorks/typesense-operator/issues/100)) ([5b1dc62](https://github.com/InterWorks/typesense-operator/commit/5b1dc624f72b343f7e43040c4623a4bc1997ca52))
* merge from source ([#24](https://github.com/InterWorks/typesense-operator/issues/24)) ([b645dfb](https://github.com/InterWorks/typesense-operator/commit/b645dfb3d5e23b47db9de3e04e8273e45df18204))


### Bug Fixes

* Add a filter for unwanted statefulset annotations(ex: rancher) ([#232](https://github.com/InterWorks/typesense-operator/issues/232)) ([ebc4fdb](https://github.com/InterWorks/typesense-operator/commit/ebc4fdbd9ced943baf2707c3ac59d45db75493ea))
* cap Typesense thread pool size in the cluster-1 sample ([5f8434c](https://github.com/InterWorks/typesense-operator/commit/5f8434c692d7e6f28c05e4af2d87143dc1f61a2e))
* clean up pre-existing golangci-lint findings ([65a2cd7](https://github.com/InterWorks/typesense-operator/commit/65a2cd725bb078f1a197edb87e02397cb0914bc2))
* clean up pre-existing golangci-lint findings ([ee75d8a](https://github.com/InterWorks/typesense-operator/commit/ee75d8a3d91ff982dddf46b077a1fe436565e77f))
* correct always-true condition in StatefulSet scale guard ([9a7d5ca](https://github.com/InterWorks/typesense-operator/commit/9a7d5ca614f054646180eb302d2843e9e3c7078d))
* **deps:** update module github.com/go-logr/logr to v1.4.4 ([#8](https://github.com/InterWorks/typesense-operator/issues/8)) ([aa9bcfa](https://github.com/InterWorks/typesense-operator/commit/aa9bcfa3f3ab39eb12df7bdd2a8c0a9d76ed1cb7))
* **deps:** update module go.uber.org/zap to v1.28.0 ([#15](https://github.com/InterWorks/typesense-operator/issues/15)) ([29a6179](https://github.com/InterWorks/typesense-operator/commit/29a617993f841ee9a9154b1661d82755ace6237f))
* **quorum:** correct ConfigMap key and bootstrap wait gate ([#253](https://github.com/InterWorks/typesense-operator/issues/253)) ([992cae7](https://github.com/InterWorks/typesense-operator/commit/992cae78407119f3114004064349272bdf8497fa))
* wrap long lines in e2e test to satisfy lll ([c33e1a8](https://github.com/InterWorks/typesense-operator/commit/c33e1a8566b505a2722939d32e0c9540e6783d77))
