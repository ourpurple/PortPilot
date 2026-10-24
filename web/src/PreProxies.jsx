import React, { useEffect, useRef, useState } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import { api, Button, IconButton, Badge, Field, Empty, Modal } from './ui.jsx';

function extractProxyName(yamlText) {
  // A single proxy from /nodes/convert is a canonical YAML mapping. Match only
  // its top-level name, never a nested option also called "name".
  const value = yamlText?.match(/^name: (.*)$/m)?.[1]?.trim();
  if (!value) return '';
  if (value.startsWith('"')) {
    try { return JSON.parse(value); } catch { return ''; }
  }
  return value.startsWith("'") && value.endsWith("'") ? value.slice(1, -1).replace(/''/g, "'") : value;
}

export function PreProxyModal({ state, mutate, notify, onClose }) {
  const [view, setView] = useState('list');
  const [editingItem, setEditingItem] = useState(null);
  const [deletingItem, setDeletingItem] = useState(null);
  const [name, setName] = useState('');
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const userEditedNameRef = useRef(false);
  const convertReqIdRef = useRef(0);

  const preProxies = state.preProxies ?? [];

  function openAdd() {
    setEditingItem(null);
    setName('');
    setContent('');
    setError('');
    userEditedNameRef.current = false;
    setView('form');
  }

  function openEdit(item) {
    setEditingItem(item);
    setName(item.name || '');
    setContent(item.config ? JSON.stringify(item.config, null, 2) : '');
    setError('');
    userEditedNameRef.current = true;
    setView('form');
  }

  function openDelete(item) {
    setDeletingItem(item);
    setError('');
    setView('delete');
  }

  useEffect(() => {
    const reqId = ++convertReqIdRef.current;
    if (view !== 'form' || editingItem || userEditedNameRef.current || name.trim() || !content.trim()) return;
    const timer = setTimeout(async () => {
      try {
        const result = await api('/nodes/convert', 'POST', { content });
        if (reqId !== convertReqIdRef.current || userEditedNameRef.current || result.count !== 1) return;
        const extracted = extractProxyName(result.yaml);
        if (extracted) setName(extracted);
      } catch {
        // The input may be incomplete. Final submission displays validation errors.
      }
    }, 400);
    return () => { clearTimeout(timer); convertReqIdRef.current++; };
  }, [content, name, view, editingItem]);

  async function handleFormSubmit(e) {
    e.preventDefault();
    if (loading) return;
    const trimmedName = name.trim();
    if (!trimmedName) {
      setError('请输入前置代理名称');
      return;
    }
    if (new TextEncoder().encode(trimmedName).length > 200) {
      setError('前置代理名称不能超过 200 字节');
      return;
    }
    const trimmedContent = content.trim();
    if (!trimmedContent) {
      setError('请输入代理配置');
      return;
    }

    setLoading(true);
    setError('');
    try {
      const priorRevision = state.revision;
      const path = editingItem ? `/pre-proxies/${editingItem.id}` : '/pre-proxies';
      const method = editingItem ? 'PUT' : 'POST';
      const result = await mutate(path, method, { name: trimmedName, content: trimmedContent });
      notify(result?.revision > priorRevision ? '应用配置后生效' : '已保存');
      setView('list');
      setEditingItem(null);
    } catch (err) {
      setError(err.message || '保存失败');
    } finally {
      setLoading(false);
    }
  }

  async function handleDeleteSubmit() {
    if (loading || !deletingItem) return;
    setLoading(true);
    setError('');
    try {
      const priorRevision = state.revision;
      const result = await mutate(`/pre-proxies/${deletingItem.id}`, 'DELETE');
      notify(result?.revision > priorRevision ? '应用配置后生效' : '已保存');
      setView('list');
      setDeletingItem(null);
    } catch (err) {
      setError(err.message || '删除失败');
    } finally {
      setLoading(false);
    }
  }

  let modalTitle = '前置代理管理';
  let modalSubtitle = '配置节点前置代理链路（PortPilot → 前置代理 → 原节点 → 目标）。';
  if (view === 'form') {
    modalTitle = editingItem ? '编辑前置代理' : '添加前置代理';
    modalSubtitle = editingItem
      ? '修改前置代理的显示名称或连接配置。'
      : '支持粘贴单节点分享链接、YAML 或 JSON。仅接受一个代理。';
  } else if (view === 'delete') {
    modalTitle = '删除前置代理';
    modalSubtitle = deletingItem ? `确认删除前置代理「${deletingItem.name}」` : '';
  }

  const deleteCount = deletingItem
    ? (state.nodes || []).filter(n => n.preProxyId === deletingItem.id).length
    : 0;

  return (
    <Modal title={modalTitle} subtitle={modalSubtitle} onClose={onClose} wide>
      {view === 'list' && (
        <>
          {!preProxies.length ? (
            <Empty
              title="暂无前置代理"
              text="添加并为节点绑定后，应用配置即可使用前置代理链路。"
              action={
                <Button variant="primary" onClick={openAdd}>
                  <Plus size={16} />添加前置代理
                </Button>
              }
            />
          ) : (
            <>
              <div className="export-meta" style={{ marginBottom: 16 }}>
                <span>共 <strong>{preProxies.length}</strong> 个前置代理</span>
                <Button variant="primary" onClick={openAdd}>
                  <Plus size={15} />添加前置代理
                </Button>
              </div>
              <div className="pre-proxy-list">
                {preProxies.map(item => {
                  const refCount = (state.nodes || []).filter(n => n.preProxyId === item.id).length;
                  return (
                    <div key={item.id} className="quick-node" style={{ padding: '12px 0' }}>
                      <div className="quick-node-name" style={{ minWidth: 0, flex: 1 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                          <strong style={{ fontSize: 14 }}>{item.name}</strong>
                          <Badge tone={item.config?.type === 'vless' ? 'violet' : item.config?.type === 'anytls' ? 'blue' : 'neutral'}>
                            {(item.config?.type || 'PROXY').toUpperCase()}
                          </Badge>
                        </div>
                        <span style={{ fontSize: 12, color: 'var(--muted, #64748b)' }}>
                          {item.config?.server ? `${item.config.server}${item.config.port ? `:${item.config.port}` : ''}` : '—'}
                          <span className="inline-divider">·</span>
                          {refCount > 0 ? `${refCount} 个节点使用` : '未被使用'}
                        </span>
                      </div>
                      <div className="row-actions">
                        <button type="button" className="row-edit" onClick={() => openEdit(item)}>
                          编辑
                        </button>
                        <IconButton title={`删除 ${item.name}`} onClick={() => openDelete(item)}>
                          <Trash2 size={16} />
                        </IconButton>
                      </div>
                    </div>
                  );
                })}
              </div>
              <div className="modal-form-footer" style={{ marginTop: 16, paddingTop: 16 }}>
                <Button onClick={onClose}>关闭</Button>
              </div>
            </>
          )}
        </>
      )}

      {view === 'form' && (
        <form onSubmit={handleFormSubmit}>
          <div className="form-grid">
            <Field label="前置代理名称" hint="显示名称用于列表和下拉框识别，不能重复，最多 200 字节。" wide>
              <input
                value={name}
                onChange={e => {
                  setName(e.target.value);
                  userEditedNameRef.current = true;
                }}
                required
                maxLength={200}
                placeholder="例如 HK-Entry / 落地前置"
                autoFocus
                disabled={loading}
              />
            </Field>
            <Field
              label="代理配置"
              hint={
                editingItem
                  ? '以 JSON 或单代理 YAML 格式修改配置。仅接受一个代理。'
                  : '支持单节点分享链接（如 vless://、ss://）、单代理 YAML 或 JSON。仅接受一个代理。'
              }
              wide
            >
              <textarea
                className="code-input"
                rows={8}
                value={content}
                onChange={e => setContent(e.target.value)}
                required
                placeholder={editingItem ? undefined : 'vless://...#前置代理\n\n或单节点 YAML / JSON'}
                disabled={loading}
                spellCheck={false}
              />
            </Field>
          </div>
          {error && <div className="inline-error" role="alert">{error}</div>}
          <div className="modal-form-footer">
            <Button type="button" onClick={() => { setView('list'); setError(''); }} disabled={loading}>
              返回列表
            </Button>
            <Button
              variant="primary"
              type="submit"
              loading={loading}
              disabled={loading || !name.trim() || !content.trim()}
            >
              {editingItem ? '保存修改' : '确认添加'}
            </Button>
          </div>
        </form>
      )}

      {view === 'delete' && (
        <div>
          <div className="confirm-symbol danger">
            <Trash2 size={25} />
          </div>
          <p className="confirm-text">
            {deleteCount > 0
              ? `此前置代理被 ${deleteCount} 个节点使用。删除后将自动解除这些节点的绑定，改为直接连接原节点服务器；应用配置后生效。`
              : '此前置代理未被任何节点使用，删除后不影响当前运行配置。'}
          </p>
          {error && <div className="inline-error" role="alert">{error}</div>}
          <div className="modal-form-footer">
            <Button type="button" onClick={() => { setView('list'); setError(''); }} disabled={loading}>
              取消
            </Button>
            <Button variant="danger-button" loading={loading} disabled={loading} onClick={handleDeleteSubmit}>
              确认删除
            </Button>
          </div>
        </div>
      )}
    </Modal>
  );
}

export function BatchPreProxyModal({ ids = [], state, mutate, notify, onClose }) {
  const [selectedProxyId, setSelectedProxyId] = useState('__UNSELECTED__');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  async function submit(e) {
    e.preventDefault();
    if (loading || selectedProxyId === '__UNSELECTED__') return;
    setLoading(true);
    setError('');
    try {
      const priorRevision = state.revision;
      const result = await mutate('/nodes/batch-pre-proxy', 'POST', {
        ids,
        preProxyId: selectedProxyId,
      });
      notify(result?.revision > priorRevision ? '应用配置后生效' : '已保存');
      onClose();
    } catch (err) {
      setError(err.message || '批量设置失败');
    } finally {
      setLoading(false);
    }
  }

  return (
    <Modal
      title="批量设置前置代理"
      subtitle={`将为已选 ${ids.length} 个节点设置前置代理。`}
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <div className="form-grid">
          <Field label="前置代理" hint="选择“无”将解除已选节点的前置代理绑定，改为直接连接原节点。" wide>
            <select
              value={selectedProxyId}
              onChange={e => { setSelectedProxyId(e.target.value); setError(''); }}
              autoFocus
              disabled={loading}
            >
              <option value="__UNSELECTED__" disabled>请选择前置代理</option>
              <option value="">无（直接连接原节点）</option>
              {(state.preProxies ?? []).map(p => (
                <option key={p.id} value={p.id}>{p.name}</option>
              ))}
            </select>
          </Field>
        </div>
        {error && <div className="inline-error" role="alert">{error}</div>}
        <div className="modal-form-footer">
          <Button type="button" onClick={onClose} disabled={loading}>取消</Button>
          <Button
            variant="primary"
            type="submit"
            loading={loading}
            disabled={loading || selectedProxyId === '__UNSELECTED__'}
          >
            确认设置
          </Button>
        </div>
      </form>
    </Modal>
  );
}

export default PreProxyModal;
