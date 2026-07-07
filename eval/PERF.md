# Performans Optimizasyon Kaydı

**Protokol:** Her değişiklik tek başına uygulanır ve aynı benchmark'la ölçülür.
Karar kuralı: süre veya çağrı/token metriğinde net iyileşme YOK → değişiklik geri alınır.
Accuracy vekilleri (layer dağılımı, unproven sayısı) kötüleşmemeli.

**Benchmark'lar:**
- `B64`: eval/paraphrased.json, host (Metal). Kod-seviyesi değişiklikler için —
  çağrı sayısı azalması donanımdan bağımsızdır.
- `D24`: eval/hard.json, Docker container (CPU-only). CPU'ya duyarlı değişiklikler
  (server bayrakları, cache) için — jüri VM'ine en yakın ortam.

**Metrikler:** duvar süresi · lokal çağrı sayısı · lokal completion token ·
layer dağılımı (deterministic/local/unproven).

| # | Değişiklik | Benchmark | Süre | Lokal çağrı | Compl. token | Layer (d/l/u) | Karar |
|---|---|---|---|---|---|---|---|
| 0 | Baseline (222b8b8) | B64 | 6m24s | 191 | 18578 | 4/53/7 | referans |
| 1 | Toplu sınıflandırma (16'lık chunk) | B64 | 8m41s | 142 | 20253 | 4/53/7 | **TUT** — çağrı −49, kategori isabeti 57→59; süre artışı batching'den değil, 2 görevin bu koşuda logic'e (3×900 tok pipeline) rotalanmasından. Ders: süreyi çağrı sayısı değil doğrulama token hacmi domine ediyor → Adım 2 |
| 2 | Doğrulama örneklemleri cevap-only (40/80 tok) | B64 | 4m33s | 142 | 9661 | 4/46/14 | **GERİ AL** — süre −%48 ama unproven 7→14: CoT'suz örneklem yetersiz, sahte uyuşmazlık → canlıda ~7 gereksiz escalation (skorlanan token!). Lokal süreyi skorlanan token'la satın almak kötü takas |
| 2b | Örneklemler kısa-CoT'lu (150/250 tok) | B64 | 5m33s | 143 | 12376 | 4/48/12 | **KISMEN** — unproven hâlâ 12. Statik seçim yerine Adım 3: süre baskısına göre dinamik mod (Full→Brief→Off) |
| 3 | Pacer: dinamik doğrulama modu (throughput projeksiyonu; Full→Brief→Off) | B64 | 6m59s | 142 | 20098 | 4/50/9(+1 brief,1 off) | **TUT** — normal bütçede ModeFull'da kalıyor: token-optimal doğrulama (unproven 9 ≈ baseline). Brief kodu yalnızca baskı altında devreye giriyor |
| 3b | Baskı testi: TOTAL_BUDGET=3m (normalin yarısından az) | B64@3m | 3m00s | 68→79 | 2997→3805 | çoğu mode=off | **TUT** — 64/64 cevaplandı, geçerli JSON. ModeOff üretim tavanı (120 tok) eklenince 'Unable' 19→8 (kalan 8 = 3 dk'nın fiziksel tabanı). Kademeli vazgeçme çalışıyor |

**Adım 2 dersi (kalıcı):** Skor = token olduğu için varsayılan daima tam-CoT doğrulama;
kısa örneklem yalnızca süre baskısında meşru. Statik hız optimizasyonu skorlanan
token'ı artırıyorsa reddedilir.

## Free logic çözücüler (2026-07-07, pivot-sonrası)

Rakip pariteси için `SolveOrdering` (sıralama, topological sort) + `SolveSyllogism`
(kıyas, reachability) eklendi — `internal/solve/logic.go`. Kategoriden bağımsız çalışır
(katı self-gate → yanlış sınıflanan bulmacayı kurtarır, eşleşmeyen metinde ateşlemez).

| Set | Kod ile çözülen logic | Not |
|---|---|---|
| tasks.json | logic-3 (Fay), logic-6 (Yes) | logic-6 factual'a sınıflanmıştı, solver kurtardı → 2 görev **0 token** |
| hard.json | 0 | lh-1/2/3 (zebra/knights/konum-ofset) güvenle devredildi |
| paraphrased.json | 0 | farklı ifade ("Name the winner", "does it follow") kalıba uymadı → güvenle devredildi |

**Karar: TUT.** Kanonik ifadeli sıralama/kıyas görevlerini 0 token'a düşürüyor, hiçbir
kapsam-dışı göreve YANLIŞ kod-cevabı vermiyor (altın kural korundu). Kapsam ifade-bağımlı
(paraphrase'lerde deferliyor) ama risk sıfır. Gerçek değer gizli jüri ifadesine bağlı.

## Batching (2026-07-07, toggle'lı — `BATCH_SIZE`)

Sentiment+factual (tek satır, ≤300 char) görevleri tek çağrıda paketle → sistem-prompt bir
kez. `internal/router/batch.go` + main.go ön-geçişi. Free-solve önce çalışır (invariant),
parse başarısızsa tek-tek'e düşer.

Mock A/B (tasks.json, tokenizer gerçekçi değil — call sayısı ölçütü geçerli, token değeri
kaba):

| Mod | Fireworks call | Token (mock) |
|---|---|---|
| BATCH_SIZE=0 | 60 | 4071 |
| BATCH_SIZE=8 | **46** (−14) | 3781 |

logic-6 (factual sınıflı) iki modda birebir aynı → free-solve invariant tuttu; boş cevap 0.

**Karar: KOD TUT, VARSAYILAN KAPALI.** Mekanizma kanıtlandı (call −%23, güvenli fallback,
invariant), ama batch modunda gerçek accuracy (bağlam karışması) ve gerçek token yalnızca
canlı Fireworks'te ölçülebilir. Mock canned cevap verdiği için accuracy ölçülemez. `BATCH_SIZE`
ladder knob olarak duruyor; canlıda token↓ + accuracy korunursa varsayılan 8 yapılacak.
