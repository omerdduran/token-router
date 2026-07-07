# AMD Developer Hackathon: ACT II — Track 1 Bilgi Dosyası

Tek doğruluk kaynağı: kurallar, skorlama, kısıtlar ve organizatör açıklamaları.
Son güncelleme: 7 Temmuz 2026.

## Künye

- **Track 1:** "Hybrid Token-Efficient Routing Agent" / "General-Purpose AI Agent"
- **Deadline:** 11 Temmuz 2026, 18:00 CEST (Cuma)
- **Ödüller:** 1. $2.500 · 2. $1.500 · 3. $1.000 + Track 1'e özel **$1.000 "Best Use of Gemma via Fireworks"**
- **Leaderboard:** https://lablab.ai/ai-hackathons/amd-developer-hackathon-act-ii/live?track=1#amd-leaderboard (7 Tem itibarıyla boş — henüz değerlendirilen submission yok)

## Skorlama

1. **Accuracy gate:** LLM-judge her cevabı "expected intent"e göre puanlar. Eşiğin altındakiler leaderboard'a giremez. Eşik değeri gizli.
2. **Token sıralaması:** Gate'i geçenler, judging proxy'nin kaydettiği **toplam token**a göre artan sıralanır. Az token = üst sıra. (Fiyat değil, ham token sayısı.)

## ⚠️ KRİTİK KURAL AÇIKLAMASI (7 Tem, organizatör Discord)

İlk duyurulardaki "lokal token 0 sayılır" ifadesi yanıltıcıydı; organizatör netleştirmesi:

- **"You cannot bundle local models in your final Docker submission."** Lokal model imaja konamaz — boyutu ne olursa olsun.
- Skorlanan **tüm inference** `FIREWORKS_BASE_URL` üzerinden, `ALLOWED_MODELS` içindeki bir modelle yapılmalı.
- Lokal inference "sıfır sayılır ve **sayılmaz**" — yani strateji aracı değil, geçersiz.
- 10GB imaj limiti kod + bağımlılıklar içindir; model ağırlığı taşınması beklenmiyor.
- Jüri container'ı sadece API çağrısı yapar → **CPU/GPU tasarımı gereksiz**.
- **Koşullu model seçimi serbest:** "You can use conditions to select different models for different tasks, as long as they are from the allowed list."
- ALLOWED_MODELS listesi değerlendirme boyunca sabit kalacak.

**Gri alan ÇÖZÜLDÜ (7 Tem, ikinci organizatör açıklaması):** "Routing intelligence
just means deciding when a task needs an LLM call (route to the cheapest
ALLOWED_MODELS model) versus **when it can be handled with plain code (zero
tokens)**. It's not local LLM vs. remote LLM." → Deterministik kod çözücüler
(Go aritmetik, kod çalıştırarak doğrulama) açıkça meşru ve yarışmanın amaçlanan
tasarımı. Kazanma formülü: düz kodla çözülebileni kodla çöz (0 token) + kalanını
en ucuz yeterli modele minimum token'la yönlendir.

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

## Kaynaklar / erişimler

- Fireworks: $50 hackathon kredisi (+ yeni ADP üyelerine $50) — Gemma modelleri app.fireworks.ai'da; organizatör Gemma 4 E4B deployment'ının $7/saat olduğunu söyledi (dev/deneme için)
- AMD AI Notebooks: team-2678, günde 4 saat GPU (ROCm/vLLM veya Unsloth+llama.cpp imajları) — Track 1 submission'ı için artık gerekli değil, Track 1 dışı deney/dev aracı
- Takım: 2 üye (me@omerduran.dev + amd.shelter597@passmail.net)

## Zaman çizelgesi / durum notları

- 7 Tem: ALLOWED_MODELS öğrenildi; lokal-first mimari kuruldu ve eval'lerle %90-97 lokal accuracy ölçüldü; ardından organizatör açıklamasıyla lokal model yasağı öğrenildi → **Fireworks-only mimariye pivot** (bkz. görev listesi #11). Leaderboard hâlâ boş — kimse geçerli submission atmadan öğrenmiş olduk.
