# basicsplit

Windows için basit bir WireGuard split tunnel aracı. Sadece seçtiğin programların, sadece seçtiğin sitelere giden trafiğini VPN'den geçirir; geri kalan her şey normal bağlantından çıkar.

Örnek: sadece `chrome.exe` içinde ve sadece `wowhead.com` için VPN.

## Nasıl çalışır

- WireGuard tüneli program içinde kurulur (`wireguard-go` + netstack). Sanal ağ adaptörü oluşturulmaz, sistemin route tablosuna dokunulmaz.
- WinDivert, kural listesindeki exe'lerin giden TCP bağlantılarını programın içindeki yerel bir dinleyiciye yönlendirir.
- Dinleyici bağlantının ilk paketinden alan adını okur (TLS SNI veya HTTP Host). Alan adı listedeyse bağlantı tünelden, değilse doğrudan çıkar.

## Kurulum

1. [Go](https://go.dev/dl/) kur ve derle:

   ```
   build.bat
   ```

2. [WinDivert 2.2.2](https://github.com/basil00/WinDivert/releases/tag/v2.2.2) paketini indir; `x64` klasöründeki `WinDivert.dll` ve `WinDivert64.sys` dosyalarını `basicsplit.exe`'nin yanına koy.
3. VPN sağlayıcından aldığın WireGuard profilini (`.conf`) aynı klasöre koy.
4. `basicsplit.example.toml` dosyasını `basicsplit.toml` adıyla kopyala, `wireguard` satırına profilinin adını yaz ve kurallarını düzenle.
5. `basicsplit.exe`'yi çalıştır. Yönetici yetkisi ister (WinDivert için gerekli), sonra sistem tepsisine yerleşir.

İlk çalıştırmada Windows Güvenlik Duvarı izin sorarsa izin ver; yakalanan bağlantılar programa gelen bağlantı olarak ulaşır.

## Kurallar

```toml
wireguard = "profil.conf"

[[rule]]
exe = "chrome.exe"
domains = ["wowhead.com", "zamimg.com"]

[[rule]]
exe = "baska.exe"   # domains yoksa exe'nin tüm TCP trafiği tünelden geçer
```

- Bir alan adı alt alan adlarını da kapsar (`wowhead.com` → `www.wowhead.com`).
- Dosyayı kaydedince kurallar birkaç saniye içinde yeniden yüklenir. `wireguard` ve `listen` değişiklikleri için programı yeniden başlat.

## Tepsi menüsü

- **Aktif:** yakalamayı açar/kapatır. Kapalıyken açık bağlantılar kopmaz.
- **Windows ile başlat:** oturum açılışında UAC sormadan başlaması için Görev Zamanlayıcı'da bir görev oluşturur.
- **Kuralları düzenle / Logu göster:** `basicsplit.toml` ve `basicsplit.log` dosyalarını açar.

## Sınırlamalar

- Sadece TCP tünellenir. UDP kullanan programlar (çoğu oyun) desteklenmez.
- Kural listesindeki exe'lerin QUIC (UDP 443) trafiği düşürülür; tarayıcı TCP'ye geçer.
- Tarayıcı alan adını şifrelerse (ECH) site eşleşmez ve doğrudan çıkar. Chrome'da "Güvenli DNS kullan" ayarını kapatmak bunu önler.
- Bazı anti-cheat sistemleri WinDivert sürücüsünden hoşlanmaz; oyun oynarken programı kapatmak daha güvenlidir.

## Kullanılanlar

- [wireguard-go](https://git.zx2c4.com/wireguard-go/)
- [WinDivert](https://github.com/basil00/WinDivert)
- [fyne.io/systray](https://github.com/fyne-io/systray)
