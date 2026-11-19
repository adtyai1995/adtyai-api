// Run `bun install --frozen-lockfile && bun run generate` from this directory.
// React Email supplies the mail-client markup. Only Go template actions, never
// customer data, enter this build; html/template escapes the actual send data.
import * as React from 'react'
import { Html, Head, Body, Container, Section, Heading, Text, Img, Link, Hr, render } from 'react-email'
import { writeFile } from 'node:fs/promises'

const text = { color: '#262b28', fontSize: '16px', lineHeight: '25px', margin: '0 0 18px', overflowWrap: 'anywhere' } as const
const detail = { ...text, color: '#4c554f', fontSize: '14px', lineHeight: '21px' }
const layout = <Html lang="en" dir="ltr"><Head><title>{'{{.Title}}'}</title></Head>
  <Body style={{ margin: 0, backgroundColor: '#ffffff', fontFamily: 'Arial, Helvetica, sans-serif' }}>
    <Container style={{ maxWidth: '560px', width: '100%', margin: '0 auto' }}>
      <Section style={{ padding: '32px 20px', tableLayout: 'fixed' }}>
        {'{{if .LogoURL}}'}<Img loading="lazy" src="{{.LogoURL}}" alt="" height="36" style={{ maxWidth: '100%', objectFit: 'contain', marginBottom: '12px' }} />{'{{end}}'}
        <Text style={{ ...text, fontSize: '18px', fontWeight: 700, marginBottom: '16px' }}>{'{{.BrandName}}'}</Text>
        <Hr style={{ borderColor: '#d9dedb', margin: '0 0 28px' }} />
        <Heading as="h1" style={{ color: '#15211a', fontSize: '27px', lineHeight: '34px', margin: '0 0 16px', overflowWrap: 'anywhere' }}>{'{{.Title}}'}</Heading>
        {'{{with .Intro}}'}<Text style={text}>{'{{.}}'}</Text>{'{{end}}'}
        {'{{with .Code}}'}<Text style={{ ...text, fontFamily: 'Consolas, Courier, monospace', fontSize: '28px', lineHeight: '40px', padding: '10px 16px', backgroundColor: '#edf4ef' }}><strong>{'{{.}}'}</strong></Text>{'{{end}}'}
        {'{{range .Paragraphs}}'}<Text style={text}>{'{{.}}'}</Text>{'{{end}}'}
        {'{{if .ActionURL}}'}<Text style={text}><Link href="{{.ActionURL}}" style={{ display: 'inline-block', backgroundColor: '#1c5138', border: '12px solid #1c5138', borderRadius: '4px', color: '#ffffff', fontWeight: 700, textDecoration: 'none' }}>{'{{.ActionLabel}}'}</Link></Text>
        <Text style={detail}>Or copy this link into your browser:<br /><span style={{ wordBreak: 'break-all' }}>{'{{.ActionURL}}'}</span></Text>{'{{end}}'}
        {'{{with .Footnote}}'}<Hr style={{ borderColor: '#d9dedb', margin: '24px 0 16px' }} /><Text style={detail}>{'{{.}}'}</Text>{'{{end}}'}
      </Section>
    </Container>
  </Body>
</Html>
const html = await render(layout, { pretty: true })
await writeFile(new URL('../../service/templates/transactional.gohtml', import.meta.url), html + '\n')
