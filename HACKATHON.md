# AMD Developer Hackathon: ACT II — Track 1 Bilgi Dosyası

Tek doğruluk kaynağı: kurallar, skorlama, kısıtlar ve organizatör açıklamaları.
Son güncelleme: 8 Temmuz 2026 (güncel resmî Participant Guide + Steve/lablab duyurusu).

## Künye

- **Track 1:** "Hybrid Token-Efficient Routing Agent" / "General-Purpose AI Agent"
- **Deadline:** 11 Temmuz 2026, 18:00 CEST (Cuma)
- **Ödüller:** 1. $2.500 · 2. $1.500 · 3. $1.000 + Track 1'e özel **$1.000 "Best Use of Gemma via Fireworks"**
- **Leaderboard:** https://lablab.ai/ai-hackathons/amd-developer-hackathon-act-ii/live?track=1#amd-leaderboard (7 Tem itibarıyla boş — henüz değerlendirilen submission yok)

## Skorlama

1. **Accuracy gate:** LLM-judge her cevabı "expected intent"e göre puanlar. Eşiğin altındakiler leaderboard'a giremez. Eşik değeri gizli.
2. **Token sıralaması:** Gate'i geçenler, judging proxy'nin kaydettiği **toplam token**a (prompt + completion) göre artan sıralanır. Az token = üst sıra. (Fiyat değil, ham token sayısı.)

### ⚠️ Token (skor) ≠ Dolar (kredi) — karıştırma
| | Neyi ölçer | Neye bağlı | Etkilediği |
|---|---|---|---|
| **Skor/sıralama** | toplam **token** | kaç token gönderip aldığın | leaderboard sıran |
| **Kredi maliyeti** | harcanan **$** | model başına $/token | $50 dev bütçen |

Model seçimi **skor açısından ~nötr** (3 Gemma aynı tokenizer → aynı string = aynı token; fark sadece gevezelik + retry sayısı). Model seçimi **dolar açısından farklı** (Kimi ~$0.95/$4.00, MiniMax ~$0.30/$1.20, Gemma ucuz per 1M) → sadece dev kredisini ilgilendirir, skoru değil.

## ⚠️⚠️ KURAL GERİ DÖNÜŞÜ (8 Tem, RESMÎ Participant Guide + Steve/lablab duyurusu)

7 Tem'deki "lokal model bundle yasak" Discord açıklaması **resmen tersine döndü.**
Güncel rehber (Rules bölümü) — bağlayıcı metin:

> **"Local models and tokens used locally count as zero for the final score;
> all Fireworks API calls must go through FIREWORKS_BASE_URL; local model
> inference inside the container is permitted and counts toward accuracy,
> but not toward the token score."**

- **Lokal model = geçerli skor stratejisi.** Lokal cevap doğruysa accuracy'ye tam
  sayılır ve 0 Fireworks token'ı — "the best possible outcome for ranking" (Steve).
- `ZERO_API_CALLS` işareti hata değil, açıkça **geçerli strateji** (rehber, s.6).
- **Grading ortamı: 4 GB RAM, 2 vCPU** (CPU-only). Rehber boyutlandırması:
  2B–3B 4-bit quantized güvenli; 7B 4-bit tüm RAM'i doldurur (agent koduna yer kalmaz).
- **Ollama/runtime önyüklü DEĞİL** — model ağırlıkları doğrudan imaja gömülmeli
  (10GB sıkıştırılmış limit içinde).
- Harness kendi `FIREWORKS_API_KEY`'ini enjekte eder — kendi key'ini imaja koyma
  (.env sadece lokal dev için).
- 7 Tem'den geçerliliğini koruyanlar: koşullu model seçimi serbest; düz-kod
  çözücüler meşru ("plain code = zero tokens"); tüm Fireworks çağrıları
  `FIREWORKS_BASE_URL` üzerinden.

**Güncel kazanma formülü:** düz kodla çözüleni kodla çöz (0 token) → kalanını
**imaja gömülü küçük lokal modelle** cevapla (0 token, accuracy'ye sayılır) →
yalnızca lokal cevabın gate'i geçemeyeceği kanıtlanan/riskli görevleri Fireworks'e
minimum token'la yönlendir. Teorik en iyi skor: gate'i geçen `ZERO_API_CALLS`.

### Troubleshooting statüleri (rehber)

`PULL_ERROR` (amd64 manifest eksik) · `RUNTIME_ERROR` (non-zero exit) ·
`TIMEOUT` (>10 dk) · `OUTPUT_MISSING` · `INVALID_RESULTS_SCHEMA` ·
`MODEL_VIOLATION` (liste dışı Fireworks modeli) · `IMAGE_TOO_LARGE` ·
`ACCURACY_GATE_FAILED`. Leaderboard ~5 dk'da güncellenir (şimdilik sadece rank).

### Resmî practice görevleri (gerçek set DEĞİL — rehber, s.3)

8 örnek: iki-parçalı factual (capital+su kütlesi), %+mutlak karışık math,
kontrastlı sentiment, tek-cümle özet, NER (kişi+şirket+yer+görece tarih),
`get_max(nums): return nums[0]` debug, **TEK-domain pet bulmacası** (Sam/Jo/Lee —
zebra çözücümüz iki-domain istiyor: kapsam boşluğu!), second-largest-with-duplicates
codegen. → eval'e eklenmeli; container I/O'yu bunlarla doğrula, submission slotu yakma.

## ALLOWED_MODELS (Track 1, launch günü yayınlandı)

| Model | Karakter | Bizim kullanım |
|---|---|---|
| `gemma-4-26b-a4b-it` | MoE 25.2B/3.8B aktif, thinking kapatılabilir | Birincil (ucuz/az laf) |
| `gemma-4-31b-it` | Dense 30.7B, thinking kapatılabilir | Zor görevler |
| `gemma-4-31b-it-nvfp4` | 31B'nin FP4 quantized hali | Yedek |
| `kimi-k2p7-code` | 1T kod modeli, reasoning-ağır | Kod son çare (thinking token'ları skora yazılır!) |
| `minimax-m3` | 428B MoE, thinking toggle | Mümkünse hiç |

## Container sözleşmesi

- Girdi: `/input/tasks.json` → `[{"task_id","prompt"}]`
- Çıktı: `/output/results.json` → `[{"task_id","answer"}]` (geçerli JSON şart; bozuksa 0 puan)
- Env (harness enjekte eder, hardcode yasak): `FIREWORKS_API_KEY`, `FIREWORKS_BASE_URL` (tüm çağrılar buradan; bypass eden çağrı kaydedilmez), `ALLOWED_MODELS` (virgülle ayrık)
- Exit 0 = başarı; hata durumunda non-zero

## Limitler ve kurallar

- İmaj: public registry'de, **linux/amd64 manifest** şart, sıkıştırılmış ≤ 10GB
- Başlama: ≤ 60 sn · Toplam koşum: ≤ 10 dk · İstek başına yanıt: ≤ 30 sn
- Cevaplar İngilizce
- Hardcode/cache yasak — değerlendirme görülmemiş prompt varyantları kullanır
- Submission: saatte 10 / takım
- 8 kategori: factual, math, sentiment, summarization, NER, code debugging, logic, code generation

## Dev workflow (organizatör önerisi)

- **Geliştirme/testi lokal modelle yap, krediyi koru.** "Keep your development and
  testing off the Fireworks API unless you want to buy more credits." Çözüm kriteri
  tutturunca (önce accuracy, sonra en az token) Fireworks'e geç.
- Uygulama: submission imajı Fireworks-only kalır; AMA aynı binary env ile lokal
  llama-server'a yönlendirilebilir (`FIREWORKS_BASE_URL=localhost:8080`,
  `ALLOWED_MODELS=local`). Client endpoint-agnostik. Dev/A-B testleri bedava lokal
  Gemma'da, sadece final doğrulama Fireworks'te.
- Uyarı: lokal Gemma E4B < gerçek Fireworks Gemma 31B. Lokal test → routing/format/
  token-sayısı güvenilir; kesin accuracy + tam token → küçük gerçek-Fireworks turu.

## Kaynaklar / erişimler

- Fireworks: $50 hackathon kredisi (+ yeni ADP üyelerine $50) — Gemma modelleri app.fireworks.ai'da; organizatör Gemma 4 E4B deployment'ının $7/saat olduğunu söyledi (dev/deneme için)
- AMD AI Notebooks: team-2678, günde 4 saat GPU (ROCm/vLLM veya Unsloth+llama.cpp imajları) — Track 1 submission'ı için artık gerekli değil, Track 1 dışı deney/dev aracı
- Takım: 2 üye (me@omerduran.dev + amd.shelter597@passmail.net)

## Zaman çizelgesi / durum notları

- 7 Tem: ALLOWED_MODELS öğrenildi; lokal-first mimari kuruldu ve eval'lerle %90-97 lokal accuracy ölçüldü; ardından organizatör açıklamasıyla lokal model yasağı öğrenildi → **Fireworks-only mimariye pivot** (bkz. görev listesi #11). Leaderboard hâlâ boş — kimse geçerli submission atmadan öğrenmiş olduk.
- 8 Tem: Resmî rehber güncellendi — **lokal model yasağı tersine döndü** (yukarıdaki bölüm). Pre-pivot lokal-first mimari (git geçmişinde) yeniden kazanan strateji; 4GB/2vCPU'ya göre yeniden boyutlandırılacak (E4B yerine muhtemelen 2B-3B Q4). Track 1 scoring pipeline canlı. 7 rakip repo analiz edildi (bkz. hafıza/rakip notları): hiçbirinde kanıt-kapılı düz-kod cevap yok; en ciddi rakip frugal-router (lokal GGUF beyin + draft-confirm) — yeni kural onların mimarisini de meşrulaştırdı.
