import { useRef, useState, type FormEvent } from 'react'
import { KeyRound, Package, Upload } from 'lucide-react'
import { api, errorMessage } from '@/lib/api'
import { useResource } from '@/lib/use-resource'
import { Feedback } from '@/components/workspace-loading'
import { Button } from '@/components/ui/button'
import { productNameForId } from '../../shared/email-products'

type PlanSummary = { id: string; productId: string; name: string; available: number; reserved: number; issued: number }
type BatchSummary = { id: string; planId: string; planName: string; count: number; available: number; reserved: number; issued: number; createdAt: number }
type Summary = { configured: boolean; plans: PlanSummary[]; batches: BatchSummary[] }

const time = (value: number) => new Date(value).toLocaleString('zh-CN', { hour12: false })

export default function VoucherInventoryPage({ revision, onUpdated }: { revision: number; onUpdated: () => void }) {
  const { data, error, loading } = useResource<Summary>('/api/admin/vouchers', revision)
  const [planID, setPlanID] = useState('')
  const [codes, setCodes] = useState('')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [actionError, setActionError] = useState('')
  const fileInput = useRef<HTMLInputElement>(null)
  const selectedPlan = planID || data?.plans[0]?.id || ''

  async function importCodes(event: FormEvent) {
    event.preventDefault()
    if (busy || !selectedPlan || !codes.trim()) return
    setBusy(true); setMessage(''); setActionError('')
    try {
      const result = await api<{ count: number }>('/api/admin/vouchers/import', { planId: selectedPlan, codes })
      setCodes('')
      if (fileInput.current) fileInput.current.value = ''
      setMessage(`已导入 ${result.count} 条卡密。卡密内容不会在后台列表中显示。`)
      onUpdated()
    } catch (e) { setActionError(errorMessage(e)) }
    finally { setBusy(false) }
  }

  async function loadFile(file?: File) {
    if (!file) return
    setActionError(''); setMessage('')
    try { setCodes(await file.text()) }
    catch { setActionError('文件读取失败，请改用 UTF-8 文本文件或直接粘贴。') }
  }

  return <>
    <section className="admin-panel">
      <div className="admin-panel-title"><h2><KeyRound size={17} /> 导入自备卡密</h2><span>每批最多 50 条</span></div>
      <div className="voucher-inventory-body">
        <p className="admin-subtle">选择现有套餐，粘贴卡密（每行一条）或读取 .txt 文件。系统会先检查整批，再加密入库；重复或格式错误会整批拒绝，不会部分导入。</p>
        {!data?.configured && data && <p className="voucher-key-status" role="status">库存加密密钥尚未配置，导入与交付已关闭。请先在本地生成并安全部署两份卡密密钥。</p>}
        <Feedback error={error} loading={loading} data={data} shape="compact" />
        <form className="voucher-import-form" onSubmit={event => void importCodes(event)}>
          <label htmlFor="voucher-plan">商品套餐</label>
          <select id="voucher-plan" value={selectedPlan} onChange={event => setPlanID(event.target.value)} disabled={!data?.plans.length || busy}>
            {data?.plans.map(plan => <option key={plan.id} value={plan.id}>{productNameForId(plan.productId)} · {plan.name}</option>)}
          </select>
          <label htmlFor="voucher-codes">卡密文本</label>
          <textarea id="voucher-codes" value={codes} onChange={event => setCodes(event.target.value)} rows={9} maxLength={32768} autoComplete="off" spellCheck={false} disabled={busy || !data?.configured} placeholder="每行粘贴一条卡密；成功导入后，输入框会自动清空。" />
          <div className="voucher-import-actions">
            <Button type="button" variant="outline" disabled={busy || !data?.configured} onClick={() => fileInput.current?.click()}><Upload size={15} />读取 .txt</Button>
            <input ref={fileInput} className="sr-only" type="file" accept=".txt,text/plain" aria-label="选择卡密文本文件" onChange={event => void loadFile(event.target.files?.[0])} />
            <Button type="submit" disabled={busy || !data?.configured || !codes.trim() || !selectedPlan}>{busy ? '正在加密导入…' : '验证并导入'}</Button>
          </div>
          {message && <p className="admin-success" role="status">{message}</p>}
          {actionError && <p className="admin-error" role="alert">{actionError}</p>}
        </form>
        <p className="admin-panel-footnote">代码只在付款核实后显示给该订单的客户。后台不提供明文回看或导出。请分批少量补货，并妥善备份独立加密密钥。</p>
      </div>
    </section>

    <section className="admin-panel">
      <div className="admin-panel-title"><h2><Package size={17} /> 套餐库存</h2><span>仅显示数量</span></div>
      <Feedback error={error} loading={loading} data={data} shape="compact" />
      {data && <div className="admin-table-scroll"><table className="admin-table"><thead><tr><th>商品套餐</th><th>可用</th><th>预留付款</th><th>已发放</th></tr></thead><tbody>
        {data.plans.map(plan => <tr key={plan.id}><td>{productNameForId(plan.productId)}<small>{plan.name} · {plan.id}</small></td><td>{plan.available}</td><td>{plan.reserved}</td><td>{plan.issued}</td></tr>)}
      </tbody></table>{!data.plans.length && <div className="admin-empty"><Package size={25} /><h3>暂无套餐</h3><p>商品套餐创建后会显示在这里。</p></div>}</div>}
    </section>

    <section className="admin-panel">
      <div className="admin-panel-title"><h2>导入批次</h2><span>不含卡密内容</span></div>
      <Feedback error={error} loading={loading} data={data} shape="compact" />
      {data && <div className="admin-table-scroll"><table className="admin-table"><thead><tr><th>套餐 / 批次</th><th>导入时间</th><th>导入数量</th><th>可用</th><th>预留</th><th>已发放</th></tr></thead><tbody>
        {data.batches.map(batch => <tr key={batch.id}><td>{batch.planName}<small>{batch.planId} · {batch.id}</small></td><td>{time(batch.createdAt)}</td><td>{batch.count}</td><td>{batch.available}</td><td>{batch.reserved}</td><td>{batch.issued}</td></tr>)}
      </tbody></table>{!data.batches.length && <div className="admin-empty"><KeyRound size={25} /><h3>还没有卡密批次</h3><p>先选择套餐并导入自备卡密。</p></div>}</div>}
    </section>
  </>
}
