import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Collapse,
  Form,
  Input,
  Modal,
  Row,
  Select,
  Space,
  Statistic,
  Switch,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import {
  IconAddRule,
  IconApplyRules,
  IconDelete,
  IconNavAudit,
  IconSaveRules,
} from '../icons';
import {
  BLOCK_TYPES,
  blockRuleLine,
  fetchBlocklist,
  saveBlocklist,
  type BlockEntry,
} from '../api/blocklist';
import { generateConfig, applyConfigYAML } from '../api/config';
import PageHeader from '../components/PageHeader';
import { useI18n } from '../i18n';
import useIsMobile from '../hooks/useIsMobile';

const errMsg = (e: any) => e?.response?.data?.error || e?.message || String(e);

const TYPE_TAG_COLORS: Record<string, string> = {
  domain: 'geekblue',
  'domain-suffix': 'blue',
  'domain-keyword': 'cyan',
  geosite: 'purple',
  'ip-cidr': 'volcano',
};

const AuditPage: React.FC = () => {
  const { t } = useI18n();
  const isMobile = useIsMobile();
  const [entries, setEntries] = useState<BlockEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [applying, setApplying] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState('');
  const [form] = Form.useForm();

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const list = await fetchBlocklist();
      setEntries(Array.isArray(list) ? list : []);
    } catch (e: any) {
      message.error(errMsg(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const stats = useMemo(() => {
    const enabled = entries.filter((e) => e.enabled);
    const domains = enabled.filter((e) => e.type !== 'ip-cidr').length;
    const ips = enabled.length - domains;
    return { total: entries.length, enabled: enabled.length, domains, ips };
  }, [entries]);

  const previewRules = useMemo(
    () => entries.filter((e) => e.enabled && e.value).map(blockRuleLine),
    [entries],
  );

  const addEntry = async () => {
    let values: BlockEntry;
    try {
      values = await form.validateFields();
    } catch {
      return; // validation messages are rendered inline
    }
    const value = String(values.value || '').trim();
    if (!value) {
      message.error(t('audit.valueRequired'));
      return;
    }
    const next = [
      ...entries,
      {
        type: values.type || '',
        value,
        remark: String(values.remark || '').trim(),
        enabled: values.enabled !== false,
      },
    ];
    setEntries(next);
    form.resetFields();
    form.setFieldsValue({ enabled: true });
    message.success(t('audit.addedUnsaved'));
  };

  const runImport = () => {
    const lines = importText
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l && !l.startsWith('#'));
    if (!lines.length) {
      message.error(t('audit.importEmpty'));
      return;
    }
    const parsed: BlockEntry[] = [];
    for (const line of lines) {
      let remark = '';
      let body = line;
      const hash = line.indexOf('#');
      if (hash >= 0) {
        remark = line.slice(hash + 1).trim();
        body = line.slice(0, hash).trim();
      }
      let type = '';
      const cut = body.indexOf(':');
      if (cut > 0) {
        const prefix = body.slice(0, cut).toLowerCase();
        if (['full', 'suffix', 'keyword', 'geosite', 'ip'].includes(prefix)) {
          type = prefix === 'full' ? 'domain' : prefix === 'suffix' ? 'domain-suffix' : prefix === 'ip' ? 'ip-cidr' : prefix;
          body = body.slice(cut + 1).trim();
        }
      }
      parsed.push({ type, value: body, remark, enabled: true });
    }
    setEntries([...entries, ...parsed]);
    setImportOpen(false);
    setImportText('');
    message.success(t('audit.importedUnsaved').replace('{n}', String(parsed.length)));
  };

  const toggleEntry = (index: number, enabled: boolean) => {
    setEntries(entries.map((e, i) => (i === index ? { ...e, enabled } : e)));
  };

  const removeEntry = (index: number) => {
    setEntries(entries.filter((_, i) => i !== index));
  };

  const persist = async () => {
    setSaving(true);
    try {
      const saved = await saveBlocklist(entries);
      setEntries(Array.isArray(saved) ? saved : entries);
      message.success(t('audit.saved'));
      return Array.isArray(saved) ? saved : entries;
    } catch (e: any) {
      message.error(errMsg(e));
      throw e;
    } finally {
      setSaving(false);
    }
  };

  const offerApply = () => {
    Modal.confirm({
      title: t('audit.applyPromptTitle'),
      content: t('audit.applyPromptBody'),
      okText: t('audit.applyNow'),
      cancelText: t('common.cancel'),
      centered: true,
      width: isMobile ? '100%' : 440,
      onOk: async () => {
        setApplying(true);
        try {
          await persist();
          const res = await generateConfig();
          await applyConfigYAML(res?.config || undefined);
          message.success(t('audit.applyDone'));
        } catch (e: any) {
          message.error(errMsg(e));
          throw e;
        } finally {
          setApplying(false);
        }
      },
    });
  };

  const columns = [
    {
      title: t('audit.colEnabled'),
      dataIndex: 'enabled',
      width: 72,
      render: (enabled: boolean, _r: BlockEntry, index?: number) => (
        <Switch
          size="small"
          checked={enabled}
          onChange={(v) => toggleEntry(Number(index), v)}
        />
      ),
    },
    {
      title: t('audit.colType'),
      dataIndex: 'type',
      width: 132,
      render: (type: string, r: BlockEntry) => (
        <Tag color={TYPE_TAG_COLORS[type || 'domain-suffix'] || 'default'}>
          {type || t('audit.typeAuto')}
        </Tag>
      ),
    },
    {
      title: t('audit.colValue'),
      dataIndex: 'value',
      render: (value: string, r: BlockEntry) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong>{value}</Typography.Text>
          {r.remark ? (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {r.remark}
            </Typography.Text>
          ) : null}
        </Space>
      ),
    },
    {
      title: t('audit.colActions'),
      key: 'actions',
      width: 72,
      render: (_v: unknown, _r: BlockEntry, index?: number) => (
        <Button
          type="text"
          danger
          size="small"
          icon={<IconDelete />}
          onClick={() => removeEntry(Number(index))}
        />
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={
          <Space>
            <IconNavAudit />
            {t('audit.title')}
          </Space>
        }
        subtitle={t('audit.subtitle')}
        extra={
          <Space wrap size="small">
            <Button onClick={load} loading={loading}>
              {t('common.refresh')}
            </Button>
            <Button icon={<IconSaveRules />} loading={saving} onClick={persist}>
              {t('audit.save')}
            </Button>
            <Button
              type="primary"
              icon={<IconApplyRules />}
              loading={applying}
              onClick={offerApply}
            >
              {t('audit.generateAndApply')}
            </Button>
          </Space>
        }
      />

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message={t('audit.scopeTitle')}
        description={t('audit.scopeBody')}
      />

      <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
        <Col xs={12} sm={6}>
          <Card size="small">
            <Statistic title={t('audit.statTotal')} value={stats.total} />
          </Card>
        </Col>
        <Col xs={12} sm={6}>
          <Card size="small">
            <Statistic title={t('audit.statEnabled')} value={stats.enabled} />
          </Card>
        </Col>
        <Col xs={12} sm={6}>
          <Card size="small">
            <Statistic title={t('audit.statDomains')} value={stats.domains} />
          </Card>
        </Col>
        <Col xs={12} sm={6}>
          <Card size="small">
            <Statistic title={t('audit.statIPs')} value={stats.ips} />
          </Card>
        </Col>
      </Row>

      <Card size="small" title={t('audit.addTitle')} style={{ marginBottom: 16 }}>
        <Form form={form} layout="vertical" initialValues={{ type: '', enabled: true }}>
          <Row gutter={12}>
            <Col xs={24} sm={8}>
              <Form.Item
                name="value"
                label={t('audit.value')}
                rules={[{ required: true, message: t('audit.valueRequired') }]}
              >
                <Input placeholder={t('audit.valuePlaceholder')} allowClear />
              </Form.Item>
            </Col>
            <Col xs={24} sm={6}>
              <Form.Item name="type" label={t('audit.type')}>
                <Select
                  options={[
                    { value: '', label: t('audit.typeAuto') },
                    ...BLOCK_TYPES.map((v) => ({ value: v, label: t(`audit.type${v.replace(/-/g, '')}`) })),
                  ]}
                />
              </Form.Item>
            </Col>
            <Col xs={24} sm={6}>
              <Form.Item name="remark" label={t('audit.remark')}>
                <Input placeholder={t('audit.remarkPlaceholder')} allowClear maxLength={64} />
              </Form.Item>
            </Col>
            <Col xs={24} sm={4}>
              <Form.Item name="enabled" label={t('audit.colEnabled')} valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
          </Row>
          <Space>
            <Button type="primary" icon={<IconAddRule />} onClick={addEntry}>
              {t('audit.add')}
            </Button>
            <Button onClick={() => setImportOpen(true)}>{t('audit.importOpen')}</Button>
          </Space>
        </Form>
      </Card>

      <Card
        size="small"
        title={t('audit.listTitle')}
        style={{ marginBottom: 16 }}
        bodyStyle={{ padding: isMobile ? 8 : undefined }}
      >
        <Table<BlockEntry>
          rowKey={(r) => `${r.type}:${r.value}`}
          loading={loading}
          dataSource={entries}
          columns={columns}
          size="small"
          pagination={
            entries.length > 20 ? { pageSize: 20, showSizeChanger: false } : false
          }
          scroll={isMobile ? { x: 480 } : undefined}
          locale={{ emptyText: t('audit.empty') }}
        />
      </Card>

      {previewRules.length > 0 && (
        <Collapse
          size="small"
          items={[
            {
              key: 'preview',
              label: t('audit.previewTitle').replace('{n}', String(previewRules.length)),
              children: (
                <Typography.Paragraph style={{ marginBottom: 0 }}>
                  <pre style={{ margin: 0, fontSize: 12, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                    {previewRules.join('\n')}
                  </pre>
                </Typography.Paragraph>
              ),
            },
          ]}
        />
      )}

      <Modal
        title={t('audit.importTitle')}
        open={importOpen}
        onOk={runImport}
        onCancel={() => setImportOpen(false)}
        okText={t('audit.importRun')}
        cancelText={t('common.cancel')}
        width={isMobile ? '100%' : 560}
        destroyOnClose
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('audit.importHintTitle')}
          description={
            <span style={{ whiteSpace: 'pre-line' }}>{t('audit.importHintBody')}</span>
          }
        />
        <Input.TextArea
          value={importText}
          onChange={(e) => setImportText(e.target.value)}
          rows={10}
          placeholder={t('audit.importPlaceholder')}
        />
      </Modal>
    </div>
  );
};

export default AuditPage;
