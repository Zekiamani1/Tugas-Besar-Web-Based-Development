# Contoh log dan span

Contoh berikut menggunakan ID dan waktu ilustratif. Nilai SQL dan parameter
disamarkan. Format serta atribut dapat berbeda sesuai operasi yang dijalankan.

```json
{"time":"2026-11-27T10:00:00Z","level":"INFO","msg":"platform database query started","event":"database.query.start","actor":"42","database":"platform","status":"started","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}
{"time":"2026-11-27T10:00:00.005Z","level":"INFO","msg":"platform database query completed","event":"database.query.result","actor":"42","database":"platform","status":"success","query":"SELECT * FROM migrations WHERE id = $[REDACTED]","duration_ms":5,"rows":1,"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}
{"time":"2026-11-27T10:00:00.006Z","level":"INFO","msg":"trace span","event":"trace.span","name":"database.query","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","duration_ms":5,"status":"Unset"}
```

`trace_id` yang sama menunjukkan hubungan peristiwa; `span_id` menunjukkan
span yang mencatat peristiwa tersebut. Jika endpoint OTLP tidak dikonfigurasi,
span diekspor sebagai log JSON ke stdout. Dengan endpoint OTLP, span dan log
dikirim ke Collector dan log JSON tetap dicetak ke stdout.

Metrik `wbd.migration.lock.wait.duration` mengukur waktu sejak percobaan lock
pertama hingga lock diperoleh, termasuk pengiriman ulang pesan jika diperlukan.
Metrik tersebut tidak mencakup job yang belum berhasil memperoleh lock dan
dapat terhitung lebih dari sekali jika pesan diproses ulang. Metrik
`wbd.migration.lock.attempt.duration` tetap mencatat durasi setiap percobaan.
