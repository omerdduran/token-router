# Token-Minimizasyon Araştırma Notları (kanıt → karar)

Derin araştırma (2023–2026 literatürü, çapraz-doğrulamalı). Her bulgu bizim
Fireworks-only mimariye somut bir karara bağlandı. Skor = accuracy gate + en az
toplam token; lokal kod bedava.

## Doğrulanmış bulgular ve kararları

### 1. PAL/PoT = en yüksek kaldıraç (KANITLANDI 3-0, arxiv 2211.10435)
Math'i Python'a offload etmek PaLM-540B CoT'u GSM8K'de +6.4pt (aynı model) geçiyor.
→ **ZATEN YAPIYORUZ** (mathPAL). Karar: EvalExpr'i genişlet (sqrt/abs/yüzde) → daha
çok problem PAL'e uysun, direct-solve fallback azalsın.

### 2. Extractive özet Lead-N > graph yöntemleri (KANITLANDI 3-0, arxiv 2512.08764)
Naive "ilk cümle" (Lead-1) çoğu küçük abstractive LLM'i VE tüm graph yöntemlerini
(TextRank/LexRank) ROUGE/BERTScore'da geçiyor. **TextRank/LexRank yazma — zaman kaybı.**
→ Karar: Özet görevlerinde pasajı API'ye göndermeden önce **lokal extractive
cümle-seçimi** yap (input token'ı ~10x kısar), sonra format-uyumu için kısa modele ver.
Pür extractive DEĞİL (bizim özetlerde "20 kelime", "tek cümle", "iki görüş" gibi katı
format var, Lead-N bunu tutmaz). Ama **büyük risk (bkz. #3)**.

### 3. ⚠️ EN BÜYÜK RİSK: extractive/kural-tabanlı çıktıyı LLM-judge kabul eder mi? (KANIT YOK)
Tüm klasik-NLP kanıtı ROUGE/BERTScore/F1 ile ölçülmüş, HİÇBİRİ LLM-judge ile değil.
→ Karar: Hiçbir kategoriyi körlemesine sıfır-token'a bağlama. Key gelince gerçek
judge prompt'una karşı A/B şart. Extractive özet ve lexicon sentiment bu kapıdan geçmeden
production'a girmez.

### 4. Prompt sıkıştırma: extractive chunk-seçimi > LLMLingua token-pruning (KANITLANDI 3-0)
Berkeley çalışması: extractive seçim 10x'e kadar minimum kayıp; LongLLMLingua sık sık
EN KÖTÜ. LLMLingua-2'nin "kayıpsız" iddiası ÇÜRÜTÜLDÜ (0-3).
→ Karar: Uzun pasajlı görevlerde (özet, belki uzun factual) lokal extractive seçim.
LLMLingua kullanma. Sadece pasajı sıkıştır, ASLA talimatı/soruyu.

### 5. Sıkıştırma reasoning görevlerinde çöküyor (KANITLANDI 3-0)
GSM8K 20x'te sadece −1.5pt AMA BBH 7x'te −13.2pt. Yüksek oran mantık bulmacalarını öldürür.
→ Karar: Logic/math pasajlarını agresif sıkıştırma; oranı muhafazakâr tut.

### 6. Sentiment'te gerekçe İSTEMEK doğruluğu DÜŞÜRÜYOR (fetch, arxiv 2406.11980)
Açıklama eklenince ChatGPT'nin "neutral" etiketi %19→%54'e sıçramış — hem token yakıyor
hem accuracy bozuyor.
→ Karar: **Sentiment'te sadece etiket** (justification kaldır) — hem token kazancı hem
olası accuracy kazancı. Şu anki promptumuz "one-sentence justification" istiyor = düzelt.
(Judge gerekçe istiyorsa geri alınır — A/B ile.)

### 7. Terse cevap factual/math'te güvenli (KANITLANDI 3-0, arxiv 2410.02736)
Factual gate'lerde kalite farkı büyük, stille oynanmıyor; verbosity bias tek-cevap
pass/fail gate'te büyük ölçüde nötr. → Karar: answer-only/no-preamble promptlar doğru,
devam. Verbosity bias'ı sömürmek için PADDING YAPMA (net-negatif).

### 8. Thinking kapatma en yüksek-güven completion tasarrufu — AMA ölç (KISMEN)
Constrained decoding'in "bedava" olduğu iddiası ÇÜRÜTÜLDÜ (0-3). Bazı instruct modeller
thinking kapalıyken bile binlerce token üretebiliyor. → Karar: Fireworks'te Gemma/MoE
thinking-off parametresini canlıda DOĞRULA + completion uzunluğunu ölç. Varsayma.

### 9. Chain of Draft: mantık gerektiğinde ultra-kısa reasoning (fetch, güçlü)
CoD, CoT completion'ını %68-92 kısıp doğruluğu koruyor (GSM8K ~200→~40 token).
→ Karar: Logic/math'in gerçekten reasoning gerektirdiği yerlerde "tam CoT" yerine
"draft" tarzı ("her adım ≤5 kelime") promptu. Completion token kazancı.

### 10. Token-metrikli routing: aynı tokenizer = aynı maliyet (DÜŞÜK güven, yapısal çıkarım)
Üç Gemma aynı tokenizer'ı paylaşır → 31b, a4b'den daha PAHALI DEĞİL (string başına aynı
token), sadece daha isabetli = daha az retry. → Karar: Varsayılanı **gemma-4-31b-it** yap
(retry'ı azaltır, token-nötr). Tek kontrol: a4b daha mı az geveze (MoE)? Canlıda completion
uzunluğu karşılaştır. Self-consistency yerine bedava lokal doğrulama — zaten öyle.

## Çürütülen (yapma) iddialar
- LLMLingua-2 "kayıpsız sıkıştırma" — 0-3 çürütüldü
- Constrained/structured decoding "accuracy-free" — 0-3 çürütüldü (gate'e karşı doğrula)
- "Agresif sıkıştırma toplam token'ı şişirir" paradoksu — 0-3 (mild oranlarda geçerli değil)

## Eyleme dönük öncelik (key durumuna göre)

**Şimdi yapılabilir (API'siz geliştir, flag arkasında A/B'ye hazır):**
- Sentiment label-only modu (env flag)
- Özet için lokal extractive cümle-ön-seçimi (pasajı kısalt, sonra modele ver)
- Chain-of-Draft promptları (logic/math)
- EvalExpr genişletme (PAL kapsamı)

**Key gelince ZORUNLU A/B (gerçek judge'a karşı):**
- Extractive özet / lexicon sentiment judge'dan geçiyor mu (#3 — en kritik)
- thinking-off gerçekten completion kısıyor mu (#8)
- 31b vs a4b completion uzunluğu (#10)
- Her terse-output değişikliğinin gate-pass etkisi
