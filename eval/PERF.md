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

## Stop sequences (2026-07-07, toggle'lı — `STOP_SEQ`)

Kategori bazlı `\n\n` stop → cevap sonrası fazladan paragraf/dolgu kesilir (completion
token). sentiment/factual/summarize/ner → `\n\n`; math/logic/code → yok (newline içerir);
batch → yok (liste `\n` ile ayrılır); PAL → `\n` (ifade tek satır). `internal/router` +
`STOP_SEQ` config.

Mock stop'u yok sayar → token tasarrufu mock'ta GÖRÜNMEZ. Regresyon kontrolü: STOP_SEQ
on/off cevaplar birebir özdeş (0 fark), boş cevap 0, hata 0 → stop parametresi hiçbir şeyi
bozmuyor. Birim test: NER/kod'da asla `\n` stop'u yok (truncation koruması).

**Karar: KOD TUT, VARSAYILAN KAPALI.** `\n\n` muhafazakâr (tek-paragraf cevabı korur), ama
gerçek completion-token azalması ve olası truncation-accuracy etkisi yalnızca canlıda
ölçülür. Ladder knob; canlıda token↓ + accuracy korunursa varsayılan açık yapılacak.

## Deneysel component seti (2026-07-07 — 7 toggle'lı component)

Hepsi ayrı `Options`/env toggle'ı; karar kuralı: **lokalde kanıtlanabilen açık,
canlı judge/tokenizer'a bağlı olan kapalı** (kod duruyor, ilk canlı turda A/B).

### PUZZLE_SOLVERS — brute-force bulmaca çözücüler (varsayılan: AÇIK)

`SolveKnights` (2^n doğruluk ataması), `SolveZebra` ((n!)² iki-nitelik atama,
sorgu-hücresi tekilliği yeter — grid'in tamamı belirsiz olsa bile), `SolvePositions`
(n! permütasyon; offset/bitişiklik — ordering çözücünün bilerek devrettiği şekiller).
Katı self-gate: parse edilemeyen kısıt cümlesi veya çoklu çözüm → defer.

| Set | Etki |
|---|---|
| hard.json | lh-1 (zebra) + lh-2 (knights) + lh-3 (pozisyon, factual'a yanlış sınıflanmışken kurtarıldı) → **0 token**; çağrı 24→21 |
| tasks.json | değişiklik yok (60 çağrı birebir) — yanlış kapma yok |
| paraphrased.json | değişiklik yok — güvenle deferliyor |

Üç cevap da elle doğrulandı. **Karar: TUT, AÇIK** (proof-safe; en pahalı logic
görevleri bedavalaştı).

### MUTATION_REPAIR — tek-nokta mutasyonla debug onarımı (varsayılan: AÇIK)

Kanıt kuralı: orijinal kod prompt'un kendi örneklerinden türetilen assert'lerde
FAIL + tam olarak BİR mutant PASS → 0 token cevap; aksi (assert yok / orijinal
geçiyor / birden çok mutant geçiyor / süre doldu) → defer. Birim testler:
`range(1,n)→range(1,n+1)` ve `a-b→a+b` onarıldı; üç defer senaryosu doğrulandı.
Eval debug görevlerinde örnek yok → hepsi defer (kapsam 0, risk 0 — mekanizma
birim testle kanıtlı, değeri gizli setin örnek içermesine bağlı). **Karar: TUT,
AÇIK** (proof-gated: yanlış cevap üretmesi yapısal olarak imkânsız).

### SOLUTION_LIB — kanıtlı çözüm kütüphanesi (varsayılan: AÇIK)

12 klasik (fibonacci ×2 konvansiyon, palindrom ×2, reverse, prime, gcd, anagram,
brackets...) istenen fonksiyon adıyla render edilip prompt'un kendi örnekleriyle
İSPATLANMADAN asla cevaplanmıyor. tasks.json code-4 (60→59 çağrı) + hard.json
ch-2 (21→20 çağrı) kütüphaneden, testli. Örneksiz/yabancı-dilli/örneği çelişen
görevler defer (birim testli). **Karar: TUT, AÇIK.**

### DEDUP — görev tekilleştirme (varsayılan: AÇIK)

Normalize (lowercase+whitespace) birebir kopyalar temsilciden kopyalanır.
Sentetik 6 görevlik set (3 kopya): 2 çağrı, kopya cevaplar özdeş ve dolu.
Eval setlerinde kopya yok → etkisiz ve risksiz. **Karar: TUT, AÇIK.**

### PROMPT_COMPRESS — girdi sıkıştırma (varsayılan: 0 = KAPALI)

Seviye 1 nezaket/boilerplate temizliği, seviye 2 + uzun özet pasajında extractive
cümle seçimi (talimat + lead her zaman korunur; dejenere çıktıda orijinale döner).
Birim testli; **judge toleransı yalnızca canlıda ölçülür** → kapalı bekliyor.

### MERGE_SYSTEM — chat şablonu tıraşlama (varsayılan: KAPALI)

System mesajı user'a gömülür → mesaj başına şablon rol-token'ları kırpılır.
Birim test + mock regresyonu temiz (mock'un canned eşleşmesi system'e bakıyordu;
merge modunda user'a da bakacak şekilde düzeltildi — mock artefaktıydı, gerçek
endpoint talimatı rolden bağımsız okur). Kazanç canlı tokenizer'da görünür → kapalı.

### GRAMMAR — kısıtlı decoding (varsayılan: KAPALI)

Sentiment'e GBNF (`response_format {type: grammar}`): dolgu token'ı üretimi
yapısal olarak imkânsız. Alan yalnızca set'liyken gövdeye yazılıyor (httptest ile
kanıtlı); hata halinde kısıtsız tek retry → cevap kaybı imkânsız. Mock alanı yok
sayar → etki canlıda ölçülür → kapalı.

**Kombinasyon dumanı:** PROMPT_COMPRESS=2 + MERGE_SYSTEM=1 + GRAMMAR=1 + STOP_SEQ=1
+ BATCH_SIZE=8, tasks.json → 45 çağrı, 64/64 dolu cevap, hata 0.

**Canlı ölçüm kuyruğu:** `BATCH_SIZE`, `STOP_SEQ`, `PROMPT_COMPRESS`,
`MERGE_SYSTEM`, `GRAMMAR`, thinking-off — hepsi mock'ta accuracy ölçülemediği
için varsayılan kapalı; ilk canlı Fireworks turunda tek tek A/B edilecek.
