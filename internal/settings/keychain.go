package settings

import (
	"errors"
	"fmt"
	"strings"
)

// 钥匙串（设置 → 常规 → "API Key 保存到系统钥匙串"）。
//
// 设计：内存中的 m.file 始终保存明文密钥，其余代码（merge、keyToPersist、Test……）完全不用关心钥匙串；
// 只在两个边界上转换：
//   - 读：NewManagerWithSecrets → resolveSecrets，把文件里的 "keychain" 引用换成真正的密钥；
//   - 写：所有写盘都经过 persist，开启钥匙串时把密钥写进钥匙串，文件里只留引用。
//
// 这样增加一个存储位置不会让每个修改设置的方法都多出一段分支。

const keychainRef = "keychain"

func keychainKey(profile string) string { return "provider/" + profile }

// KeychainStatus 是设置页显示的钥匙串状态。
type KeychainStatus struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	// Warning 非空表示启动时有密钥没能从钥匙串读出（例如钥匙串被锁定），对应的厂商暂时不可用。
	Warning string `json:"warning,omitempty"`
}

// resolveSecrets 在启动时把钥匙串引用解析为密钥。读不出来的条目记在 unresolved 里：
// 之后保存设置时保留它的引用，不会因为一次读取失败就把用户的密钥弄丢。
func (m *Manager) resolveSecrets() {
	m.inKeychain, m.unresolved = map[string]bool{}, map[string]bool{}
	var failed []string
	for i, p := range m.file.Providers {
		if p.APIKeyRef != keychainRef {
			continue
		}
		m.file.Providers[i].APIKeyRef = ""
		m.inKeychain[p.Name] = true
		if m.secrets == nil {
			m.unresolved[p.Name] = true
			failed = append(failed, p.Name)
			continue
		}
		key, err := m.secrets.Get(keychainKey(p.Name))
		if err != nil {
			m.unresolved[p.Name] = true
			failed = append(failed, p.Name)
			continue
		}
		m.file.Providers[i].APIKey = key
	}
	if len(failed) > 0 {
		m.keyWarning = fmt.Sprintf("没能从系统钥匙串读出以下厂商的 API Key：%s。请检查钥匙串是否已解锁，或在厂商设置中重新填写。", strings.Join(failed, "、"))
	}
}

// persist 写入设置文件。开启钥匙串时，密钥写进钥匙串，文件里只留引用。调用方持有 m.mu。
//
// 顺序：先写钥匙串，再写文件，最后删除不再需要的钥匙串条目。任何一步失败，磁盘上的状态都是一致的
// （最坏情况是钥匙串里多一个没人引用的条目）。
func (m *Manager) persist(next File) error {
	out := cloneFile(next)
	stored := map[string]bool{}
	if next.UseKeychain {
		if m.secrets == nil {
			return errors.New("当前环境不支持系统钥匙串")
		}
		for i, p := range out.Providers {
			switch {
			case p.APIKey != "":
				if err := m.secrets.Set(keychainKey(p.Name), p.APIKey); err != nil {
					return fmt.Errorf("保存到系统钥匙串失败：%w", err)
				}
			case m.unresolved[p.Name]:
				// 没读出来的密钥原样保留引用。
			default:
				continue
			}
			out.Providers[i].APIKey, out.Providers[i].APIKeyRef = "", keychainRef
			stored[p.Name] = true
		}
	} else {
		// 关闭钥匙串：读不出来的条目只能保留引用（没有明文可写），等用户重新填写。
		for i, p := range out.Providers {
			if p.APIKey == "" && m.unresolved[p.Name] {
				out.Providers[i].APIKeyRef = keychainRef
				stored[p.Name] = true
			}
		}
	}
	if err := Save(m.path, out); err != nil {
		return err
	}
	for name := range m.inKeychain {
		if !stored[name] && m.secrets != nil {
			_ = m.secrets.Delete(keychainKey(name))
		}
	}
	m.inKeychain = stored
	// 用户重新填写了密钥（或删除了配置档）之后，就不再是"未解析"了。
	for name := range m.unresolved {
		if !stored[name] {
			delete(m.unresolved, name)
		}
	}
	for _, p := range next.Providers {
		if p.APIKey != "" {
			delete(m.unresolved, p.Name)
		}
	}
	return nil
}

// Keychain 返回钥匙串状态。
func (m *Manager) Keychain() KeychainStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := KeychainStatus{Enabled: m.file.UseKeychain, Warning: m.keyWarning}
	switch {
	case m.secrets == nil:
		st.Error = "当前环境不支持系统钥匙串"
	default:
		if err := m.secrets.Available(); err != nil {
			st.Error = err.Error()
		} else {
			st.Available = true
		}
	}
	return st
}

// SetUseKeychain 开启或关闭钥匙串，并立即把已有的密钥迁移过去（或迁回设置文件）。
func (m *Manager) SetUseKeychain(on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if on {
		if m.secrets == nil {
			return errors.New("当前环境不支持系统钥匙串")
		}
		if err := m.secrets.Available(); err != nil {
			return err
		}
	}
	next := cloneFile(m.file)
	next.UseKeychain = on
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}
