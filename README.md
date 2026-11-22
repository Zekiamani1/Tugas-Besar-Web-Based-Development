# Milestone 1 Tugas Besar IF3110 Web-Based Development

## Anggota Kelompok

- 13524058 Muhammad Naufal Romi Annafi
- 13524082 Zeki Amani
- 13524133 Muhammad Daffa Arrizki Yanma

## Deskripsi

Database Manager adalah aplikasi web untuk mengelola akses ke database target bersama (PostgreSQL, MySQL, dan SQLite). Platform Admin dapat mengatur pengguna, group, koneksi database, dan izin group. Pengguna dapat menjelajahi database yang diizinkan serta membuat, meninjau, dan menjalankan migration.

## Fitur

- Login menggunakan username dan password; akun pengguna dibuat oleh Platform Admin.
- Pengelolaan pengguna dan group, termasuk melihat anggota setiap group.
- Pemberian izin database kepada group dengan tingkat **View Only** atau **View + Edit**.
- Ringkasan database yang dapat diakses setiap group.
- Database viewer untuk melihat schema, tabel, kolom, constraint, index, dan data.
- Pembuatan dan pengubahan draft migration sebelum dieksekusi.
- Eksekusi migration dalam transaksi pada database target, dengan rollback jika SQL gagal.
- Riwayat migration yang mencatat versi, SQL, pelaksana, waktu, dan status.
- Session dengan idle timeout, absolute expiration, rotasi ID, dan grace period.
- Perlindungan CSRF untuk form browser.

## Struktur Program

```text
.
├── app.go                               # Routing, handler, dan integrasi service
├── main.go                              # Konfigurasi dan entry point server
├── internal/
│   ├── middleware/                      # Auth, otorisasi, dan CSRF
│   ├── models/                          # Model data dan ringkasan untuk UI
│   └── service/
│       ├── accesscontrol/               # User, session, group, dan izin
│       ├── dbmanagement/                # Koneksi dan database viewer
│       ├── interfaces/                  # Kontrak antarservice
│       └── migration/                   # Draft, eksekusi, dan riwayat migration
├── web/
│   ├── static/                          # CSS dan halaman login
│   └── templates/                       # Template halaman aplikasi
├── *test.go                             # Unit, integration, dan E2E test
├── docker-compose.yml                   # App dan PostgreSQL internal
├── Dockerfile
├── .env.example                         # Contoh konfigurasi lingkungan
├── go.mod                               # Modul dan dependensi Go
└── README.md
```

## Prasyarat

- Go 1.26.8 atau lebih baru.
- Docker Engine dan Docker Compose untuk menjalankan aplikasi serta integration test.
- Browser web modern.

## Cara Setup dan Menjalankan

Jalankan perintah berikut dari direktori utama repository:

```bash
cp .env.example .env
```

Sebelum menjalankan aplikasi:

1. Ubah `ADMIN_PASSWORD` dan password PostgreSQL pada `.env`.
2. Pastikan password pada `DATABASE_URL` sama dengan `POSTGRES_PASSWORD`.
3. Untuk penggunaan lokal melalui HTTP, atur `SESSION_COOKIE_SECURE=false`. Gunakan `true` saat aplikasi diakses melalui HTTPS.

Jalankan aplikasi dan PostgreSQL internal:

```bash
docker compose up --build
```

Buka [http://localhost:8080](http://localhost:8080), lalu login menggunakan `ADMIN_USERNAME` dan `ADMIN_PASSWORD` dari `.env`. Port host dapat diubah melalui `APP_PORT`.

Saat mendaftarkan PostgreSQL atau MySQL melalui Admin Panel, gunakan host dan port yang dapat dijangkau oleh container aplikasi.

### Batasan pendaftaran SQLite

Backend menerima database SQLite dengan path file pada field `db_name`, tanpa host, port, user, atau password. Namun, form Admin saat ini mewajibkan semua kolom koneksi tersebut, sehingga SQLite **belum dapat didaftarkan melalui antarmuka browser**. Pendaftaran SQLite hanya dapat dilakukan melalui request JSON ke `POST /admin/databases` oleh Platform Admin yang sudah login. Jangan mengisi nilai koneksi palsu pada form sebagai jalan pintas.

Untuk menghentikan aplikasi, tekan `Ctrl+C`. Data PostgreSQL internal disimpan dalam Docker volume.

## Penyimpanan migration: tahap awal M2

Definisi kini disimpan di `migrations`, sedangkan setiap attempt disimpan di
`migration_executions`. Attempt memiliki execution ID, revisi definisi, snapshot
SQL, pelaksana, waktu mulai/selesai, status, dan error. Eksekusi tidak memperbarui
definisi SQL. Setelah memiliki attempt, definisi tidak dapat diedit lagi.
`MigrationRecord` dipertahankan sebagai bentuk respons untuk handler/template M1,
bukan model penyimpanan baru. `GetLinearHistory` membaca snapshot setiap execution;
`GetExecutionHistory` pada service menyediakan data attempt lengkap.

Saat startup, `Initialize` membuat tabel baru dan mengimpor data `migration_records`
secara transactional. ID migration, SQL, versi eksekusi, pelaksana, waktu, serta
status M1 dipertahankan. Draft tidak menghasilkan execution. Inisialisasi berulang
tidak menggandakan execution atau menimpa draft yang sudah diubah. Tabel lama
tetap disimpan sebagai arsip dan tidak lagi ditulis oleh service. Hentikan instance
aplikasi versi lama sebelum menjalankan upgrade ini.

Data M1 tidak memiliki SQL down, sehingga ditandai irreversible dengan alasan.
API M1 yang hanya menerima `sql_query` juga membuat draft irreversible secara
eksplisit. API JSON menerima SQL down atau penanda irreversible dengan alasan.
Service menyediakan `GetActiveMigrations` dan `ExecuteReversal`. Worker harus
memeriksa akses dan approval sebelum memanggil `ExecuteReversal` dengan ID
execution yang disetujui: execution aktif terakhir untuk rollback atau execution
rollback sukses untuk forward. Apply baru yang sukses menutup jalur forward lama
agar riwayat tetap linear. Approval dan penyambungan queue/worker masih diperlukan.
Alur execute HTTP M1 masih dipertahankan sementara untuk kompatibilitas.

Attempt `RUNNING` disimpan sebelum SQL target dijalankan. Reservasi ini mencegah
eksekusi bersamaan pada database yang sama. Pemanggil menerima `ErrDatabaseBusy`
jika ada attempt `RUNNING` atau `UNCERTAIN`; pemilik worker perlu menjadwalkan
ulang job tersebut. Database lain tidak ikut diblokir. Jika commit target tidak
pasti, attempt menjadi `UNCERTAIN`; jika penulisan hasil gagal, reservasi `RUNNING`
tetap tersimpan. Kedua kondisi menahan eksekusi berikutnya. Rekonsiliasi bukti
commit dan pemulihan setelah restart belum diimplementasikan; jangan mengulang
SQL atau menghapus reservasi tanpa memeriksa hasil pada database target.

Jalankan unit test modul:

```bash
go test ./internal/service/migration -v
```

Untuk menjalankan juga integration test storage PostgreSQL, set `MIGRATION_TEST_DSN`
ke URL database disposable khusus testing. Test membuat schema unik dan
menghapusnya setelah selesai:

```bash
MIGRATION_TEST_DSN='postgres://test_user:test_password@localhost:5432/test_db?sslmode=disable' go test ./internal/service/migration -v
```
